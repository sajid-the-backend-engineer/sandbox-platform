/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Injectable, Logger } from '@nestjs/common'
import { Cron, CronExpression } from '@nestjs/schedule'
import { InjectRepository } from '@nestjs/typeorm'
import { InjectRedis } from '@nestjs-modules/ioredis'
import Redis from 'ioredis'
import { FindOptionsWhere, In, MoreThanOrEqual, Not, Repository } from 'typeorm'
import { TypedConfigService } from '../../config/typed-config.service'
import { RunnerAsleepError, RunnerStartingError } from '../../exceptions/runner-starting.exception'
import { RedisLockProvider } from '../common/redis-lock.provider'
import { Job } from '../entities/job.entity'
import { Runner } from '../entities/runner.entity'
import { Sandbox } from '../entities/sandbox.entity'
import { SnapshotRunner } from '../entities/snapshot-runner.entity'
import { BackupState } from '../enums/backup-state.enum'
import { JobStatus } from '../enums/job-status.enum'
import { JobType } from '../enums/job-type.enum'
import { RunnerPowerState } from '../enums/runner-power-state.enum'
import { RunnerState } from '../enums/runner-state.enum'
import { SandboxDesiredState } from '../enums/sandbox-desired-state.enum'
import { SandboxState } from '../enums/sandbox-state.enum'
import { SnapshotRunnerState } from '../enums/snapshot-runner-state.enum'
import { SandboxRepository } from '../repositories/sandbox.repository'
import { RunnerPowerEcsClient } from './runner-power-ecs.client'

/**
 * Request header that lets a caller opt out of waking the runner. With the value
 * `never`, a request that would wake the runner is answered with RUNNER_ASLEEP
 * instead, and does not count as activity.
 */
export const RUNNER_WAKE_HEADER = 'X-Runner-Wake'

export interface EnsureAwakeOptions {
  // False when the caller asked not to start the runner for this request
  // (X-Runner-Wake: never): it goes through only if the runner is already up.
  wake?: boolean
}

/**
 * Reads the X-Runner-Wake header. Name and value are case-insensitive; a header
 * sent twice arrives joined with a comma.
 */
export function runnerWakeOptionsFromHeader(value: string | string[] | undefined): EnsureAwakeOptions {
  const values = (Array.isArray(value) ? value.join(',') : (value ?? '')).split(',')
  return { wake: !values.some((v) => v.trim().toLowerCase() === 'never') }
}

const KEY_STATE = 'runner-power:state'
const KEY_STATE_SINCE = 'runner-power:state-since'
const KEY_LAST_ACTIVITY = 'runner-power:last-activity'
const KEY_LAST_BUSY = 'runner-power:last-busy'
const KEY_WAKE_REQUESTED_AT = 'runner-power:wake-requested-at'
const KEY_WAKE_TIMEOUT_LOGGED_AT = 'runner-power:wake-timeout-logged-at'
const KEY_BUSY_LOGGED = 'runner-power:busy-logged'

// Held across every state change, so a wake can never interleave with a sleep
// that is half done, and the ECS reconcile never acts on a half-made change.
const LOCK_TRANSITION = 'runner-power:transition'
const LOCK_CHECK = 'runner-power:check'

// A runner that has not reported for this long is not counted as up. Same
// threshold the check-runners job uses to mark a runner UNRESPONSIVE.
const RUNNER_REPORT_FRESH_MS = 60_000

// A runner job that has not moved for this long is not counted: the runner has
// dropped it, and it must not hold the runner on, or wake it, for ever.
const JOB_STALE_MS = 6 * 60 * 60_000

// How often, at most, the reason the runner stays on is logged.
const BUSY_LOG_INTERVAL_SECONDS = 60 * 60

// Sandboxes in these states are running, or are being changed by the runner.
// PAUSED is here because a paused container's memory lives on the host: switching
// the host off would lose it.
const BUSY_SANDBOX_STATES = [
  SandboxState.STARTED,
  SandboxState.CREATING,
  SandboxState.RESTORING,
  SandboxState.DESTROYING,
  SandboxState.STARTING,
  SandboxState.STOPPING,
  SandboxState.PENDING_BUILD,
  SandboxState.BUILDING_SNAPSHOT,
  SandboxState.PULLING_SNAPSHOT,
  SandboxState.ARCHIVING,
  SandboxState.RESIZING,
  SandboxState.SNAPSHOTTING,
  SandboxState.FORKING,
  SandboxState.PAUSING,
  SandboxState.PAUSED,
  SandboxState.RESUMING,
]

// States that never converge on their own. A sandbox stuck here must not keep the
// runner awake forever, or wake it.
const SETTLED_SANDBOX_STATES = [
  SandboxState.ERROR,
  SandboxState.BUILD_FAILED,
  SandboxState.UNKNOWN,
  SandboxState.DESTROYED,
]

// Queued runner work someone is waiting on now, which wakes a sleeping runner.
// Stops, deletes, pauses, image removals and network-setting changes are left for
// the next wake, like the sandbox requests behind them.
const WAKE_JOB_TYPES = [
  JobType.CREATE_SANDBOX,
  JobType.START_SANDBOX,
  JobType.RECOVER_SANDBOX,
  JobType.RESIZE_SANDBOX,
  JobType.CREATE_BACKUP,
  JobType.SNAPSHOT_SANDBOX,
  JobType.FORK_SANDBOX,
  JobType.BUILD_SNAPSHOT,
  JobType.PULL_SNAPSHOT,
  JobType.INSPECT_SNAPSHOT_IN_REGISTRY,
]

/**
 * Switches the runner off when nothing needs it, and back on when something does.
 *
 * Off unless RUNNER_POWER_MANAGEMENT_ENABLED is true; with it off every method
 * here is a no-op and the platform behaves exactly as upstream.
 *
 * The only lever is the runner service's desired count (see RunnerPowerEcsClient).
 * At zero, ECS managed scaling removes the empty host; at one, it starts a host,
 * which attaches the runner's kept data volume, so parked sandboxes and the image
 * cache survive the sleep. ECS is the truth: the state kept in Redis is checked
 * against it on every tick.
 *
 * The race that matters is a request arriving just as the runner goes to sleep.
 * A request records "activity now" BEFORE it reads the power state; the sleeper
 * publishes SLEEPING BEFORE it re-reads the activity time. Whichever runs second
 * sees the other, so a request either cancels the sleep or finds the runner asleep
 * and wakes it. It never lands on a runner that is shutting down. (A request sent
 * with X-Runner-Wake: never records nothing; see ensureAwake.)
 */
@Injectable()
export class RunnerPowerService {
  private readonly logger = new Logger(RunnerPowerService.name)

  constructor(
    private readonly configService: TypedConfigService,
    private readonly ecs: RunnerPowerEcsClient,
    private readonly redisLockProvider: RedisLockProvider,
    @InjectRedis()
    private readonly redis: Redis,
    private readonly sandboxRepository: SandboxRepository,
    @InjectRepository(Runner)
    private readonly runnerRepository: Repository<Runner>,
    @InjectRepository(Job)
    private readonly jobRepository: Repository<Job>,
    @InjectRepository(SnapshotRunner)
    private readonly snapshotRunnerRepository: Repository<SnapshotRunner>,
  ) {}

  isEnabled(): boolean {
    return this.configService.get('runnerPower.enabled') === true
  }

  /**
   * Call before any request that needs a running runner (create, start, recover,
   * resize, snapshot, backup).
   *
   * Returns when the runner is up. Otherwise starts it if it is not already
   * starting, and throws RunnerStartingError for the caller to retry. With
   * `wake: false` it starts nothing, records no activity, and throws
   * RunnerAsleepError instead.
   */
  async ensureAwake(reason: string, options: EnsureAwakeOptions = {}): Promise<void> {
    if (!this.isEnabled()) {
      return
    }

    const mayWake = options.wake !== false

    // Order matters: see the class comment. A request that may not wake the runner
    // is not activity, so it neither delays a sleep nor counts toward a wake. One
    // that reads AWAKE just as a sleep starts can still land on a stopping runner;
    // the sandbox it queued is then picked up by the ASLEEP safety net.
    if (mayWake) {
      await this.redis.set(KEY_LAST_ACTIVITY, String(Date.now()))
    }

    let state: RunnerPowerState
    try {
      state = await this.currentState()
    } catch (error) {
      // Not knowing is not a reason to refuse work. Behave as if the feature were
      // off; the next check tick sorts the state out.
      this.logger.error(`Could not read runner power state; letting the request through: ${error}`)
      return
    }

    if (state === RunnerPowerState.AWAKE) {
      return
    }

    if (
      state === RunnerPowerState.WAKING &&
      (await this.runnerReportedSince(await this.getNumber(KEY_WAKE_REQUESTED_AT)))
    ) {
      try {
        await this.markAwake()
      } catch (error) {
        // The runner has reported, so the request can go ahead; the next check
        // tick records the state.
        this.logger.warn(`Could not mark the runner awake: ${error}`)
      }
      return
    }

    if (!mayWake) {
      throw new RunnerAsleepError(this.retryAfterSeconds())
    }

    let after: RunnerPowerState
    try {
      after = await this.wake(reason)
    } catch (error) {
      this.logger.error(`Could not wake the runner for "${reason}": ${error}`)
      throw new RunnerStartingError(this.retryAfterSeconds())
    }

    if (after === RunnerPowerState.AWAKE) {
      return
    }
    throw new RunnerStartingError(this.retryAfterSeconds())
  }

  /**
   * Ask ECS for the runner task again. Idempotent: a runner that is already
   * awake or waking is left alone, so ten requests at once start one host.
   */
  async wake(reason: string): Promise<RunnerPowerState> {
    return this.withTransitionLock(async () => {
      const state = await this.getState()
      if (state === RunnerPowerState.AWAKE || state === RunnerPowerState.WAKING) {
        return state
      }

      await this.ecs.setDesiredCount(1)
      await this.redis.set(KEY_WAKE_REQUESTED_AT, String(Date.now()))
      await this.redis.del(KEY_WAKE_TIMEOUT_LOGGED_AT)
      await this.setState(RunnerPowerState.WAKING)
      this.logger.log(`Waking the runner: ${reason}`)
      return RunnerPowerState.WAKING
    })
  }

  @Cron(CronExpression.EVERY_30_SECONDS, { name: 'runner-power-check', waitForCompletion: true })
  async handleCheck(): Promise<void> {
    if (!this.isEnabled()) {
      return
    }

    const hasLock = await this.redisLockProvider.lock(LOCK_CHECK, 60)
    if (!hasLock) {
      return
    }

    try {
      const state = await this.reconcile()
      switch (state) {
        case RunnerPowerState.AWAKE:
          await this.sleepIfIdle()
          break
        case RunnerPowerState.WAKING:
          await this.checkWake()
          break
        case RunnerPowerState.ASLEEP: {
          // Safety net. The request paths wake the runner themselves; anything
          // that reached the queue another way is picked up here.
          const reason = await this.findWakeReason()
          if (reason) {
            await this.wake(reason)
          }
          break
        }
        // SLEEPING here means a previous tick died mid-transition and ECS could
        // not be read this time; the next reconcile settles it. null means a wake
        // or a sleep holds the transition lock, and the next tick looks again.
      }
    } catch (error) {
      this.logger.error(`Runner power check failed: ${error}`)
    } finally {
      await this.redisLockProvider.unlock(LOCK_CHECK)
    }
  }

  /**
   * Brings the state in Redis in line with ECS. The two drift apart when someone
   * changes the desired count by hand (the documented manual wake), when a
   * transition dies halfway, or when Redis loses the key. One DescribeServices
   * call per tick.
   *
   * Returns the state the rest of the tick works from, or null to skip the tick.
   */
  private async reconcile(): Promise<RunnerPowerState | null> {
    // Read both sides under the lock every transition holds, so a wake that has
    // set the desired count but not yet the state is never mistaken for drift.
    if (!(await this.redisLockProvider.lock(LOCK_TRANSITION, 60))) {
      return null
    }

    try {
      const stored = await this.readStoredState()
      let desiredCount: number
      try {
        desiredCount = await this.ecs.getDesiredCount()
      } catch (error) {
        if (!stored) {
          throw error
        }
        this.logger.warn(`Could not read the runner service from ECS; keeping the stored power state: ${error}`)
        return stored
      }
      return await this.applyEcsState(stored, desiredCount)
    } finally {
      await this.redisLockProvider.unlock(LOCK_TRANSITION)
    }
  }

  private async sleepIfIdle(): Promise<void> {
    const now = Date.now()

    // Before the minimum-awake check, so the quiet period runs from the moment
    // the last sandbox stopped, even when that was early in the awake period.
    const busy = await this.findBusyReason()
    if (busy) {
      await this.redis.set(KEY_LAST_BUSY, String(now))
      if (await this.redis.set(KEY_BUSY_LOGGED, String(now), 'EX', BUSY_LOG_INTERVAL_SECONDS, 'NX')) {
        this.logger.log(`Runner kept on: ${busy}`)
      }
      return
    }

    const awakeSince = await this.getNumber(KEY_STATE_SINCE)
    if (now - awakeSince < this.minutes('runnerPower.minAwakeMinutes')) {
      return
    }

    const quietSince = Math.max(
      awakeSince,
      await this.getNumber(KEY_LAST_ACTIVITY),
      await this.getNumber(KEY_LAST_BUSY),
    )
    const idleMs = this.minutes('runnerPower.idleMinutes')
    if (now - quietSince < idleMs) {
      return
    }

    const hasLock = await this.redisLockProvider.lock(LOCK_TRANSITION, 60)
    if (!hasLock) {
      return
    }

    try {
      if ((await this.getState()) !== RunnerPowerState.AWAKE) {
        return
      }

      await this.setState(RunnerPowerState.SLEEPING)

      // Re-read AFTER publishing SLEEPING. A request that recorded activity before
      // this point shows up here; one that records it later reads SLEEPING and wakes.
      const lastActivity = await this.getNumber(KEY_LAST_ACTIVITY)
      const busyNow = (await this.findBusyReason()) ?? (await this.findDeployReason())
      if (Date.now() - lastActivity < idleMs || busyNow) {
        this.logger.log(`Runner sleep cancelled: ${busyNow ?? 'a request arrived'}`)
        if (busyNow) {
          await this.redis.set(KEY_LAST_BUSY, String(Date.now()))
        }
        await this.setState(RunnerPowerState.AWAKE)
        return
      }

      try {
        await this.ecs.setDesiredCount(0)
      } catch (error) {
        // The call can fail after ECS has taken it (a timeout, a lost reply), so
        // ask ECS where it ended up rather than assume. If that cannot be read
        // either, assume the runner is still on; the next reconcile corrects it.
        if (!(await this.desiredCountIsZero())) {
          await this.setState(RunnerPowerState.AWAKE)
          throw error
        }
        this.logger.warn(`ECS returned an error, but the runner service is at zero: ${error}`)
      }
      await this.setState(RunnerPowerState.ASLEEP)
      this.logger.log(`Runner switched off after ${Math.round((now - quietSince) / 60_000)} idle minutes`)
    } finally {
      await this.redisLockProvider.unlock(LOCK_TRANSITION)
    }
  }

  /**
   * Why the runner must not be switched off right now on account of a deploy, or
   * null. Asked last, just before the switch-off, so it costs one ECS call per
   * sleep rather than one per tick. Not knowing counts as a reason: a sleep that
   * waits one more tick loses nothing, and one that lands mid-deploy hides a
   * broken image from the deploy's own checks.
   */
  private async findDeployReason(): Promise<string | null> {
    try {
      return (await this.ecs.isDeploying()) ? 'the runner service is being deployed' : null
    } catch (error) {
      return `the runner service's deployments could not be read (${error})`
    }
  }

  private async checkWake(): Promise<void> {
    const wakeRequestedAt = await this.getNumber(KEY_WAKE_REQUESTED_AT)
    if (await this.runnerReportedSince(wakeRequestedAt)) {
      if (await this.markAwake()) {
        this.logger.log(`Runner is up, ${Math.round((Date.now() - wakeRequestedAt) / 1000)} s after the wake request`)
      }
      return
    }

    const timeoutMs = this.minutes('runnerPower.wakeTimeoutMinutes')
    if (Date.now() - wakeRequestedAt < timeoutMs) {
      return
    }

    // Log once per timeout window, not every tick, and ask again in case the
    // first request was lost. Setting the same count twice is harmless.
    const loggedAt = await this.getNumber(KEY_WAKE_TIMEOUT_LOGGED_AT)
    if (Date.now() - loggedAt >= timeoutMs) {
      this.logger.error(
        `RUNNER_WAKE_TIMEOUT: the runner has not reported ${Math.round((Date.now() - wakeRequestedAt) / 60_000)} minutes after it was woken`,
      )
      await this.redis.set(KEY_WAKE_TIMEOUT_LOGGED_AT, String(Date.now()))
      await this.ecs.setDesiredCount(1)
    }
  }

  /**
   * WAKING -> AWAKE once the runner has reported. Re-reads the state under the
   * lock, since a reconcile or another request may have moved it on. Returns
   * whether this call made the change.
   */
  private async markAwake(): Promise<boolean> {
    return this.withTransitionLock(async () => {
      if ((await this.getState()) !== RunnerPowerState.WAKING) {
        return false
      }
      await this.setState(RunnerPowerState.AWAKE)
      return true
    })
  }

  /**
   * Why the runner must stay on, naming one sandbox, job or image as an example,
   * or null when nothing needs it.
   */
  private async findBusyReason(): Promise<string | null> {
    const busySandbox = await this.findSandboxId({ state: In(BUSY_SANDBOX_STATES) })
    if (busySandbox) {
      return `sandbox ${busySandbox} is running or changing state`
    }

    const pendingSandbox = await this.findSandboxId({ pending: true })
    if (pendingSandbox) {
      return `sandbox ${pendingSandbox} has a pending change`
    }

    // A requested change the runner has not carried out yet: a delete, a stop, an
    // archive. Finishing it before sleeping keeps the queue short at the next wake.
    const unconverged = await this.sandboxRepository
      .createQueryBuilder('sandbox')
      .select('sandbox.id')
      .where('sandbox.state NOT IN (:...settled)', { settled: SETTLED_SANDBOX_STATES })
      .andWhere('sandbox."desiredState"::text != sandbox.state::text')
      .getOne()
    if (unconverged) {
      return `sandbox ${unconverged.id} has not reached its requested state`
    }

    const backingUp = await this.findSandboxId({
      backupState: In([BackupState.PENDING, BackupState.IN_PROGRESS]),
      state: In([SandboxState.ARCHIVING, SandboxState.STARTED, SandboxState.STOPPED]),
      desiredState: Not(SandboxDesiredState.DESTROYED),
    })
    if (backingUp) {
      return `sandbox ${backingUp} has a backup in progress`
    }

    const job = await this.findLiveJob([JobStatus.PENDING, JobStatus.IN_PROGRESS])
    if (job) {
      return `runner job ${job.id} (${job.type}) is queued or running`
    }

    // Only image work a runner that is up can actually do. A removal queued for a
    // runner that is down stays queued (see SnapshotManager), and counting it would
    // keep this runner on for work that cannot progress.
    const readyRunners = await this.runnerRepository.find({
      where: { state: RunnerState.READY },
      select: { id: true },
    })
    const snapshotRunner =
      readyRunners.length === 0
        ? null
        : await this.snapshotRunnerRepository.findOne({
            where: {
              state: In([
                SnapshotRunnerState.PULLING_SNAPSHOT,
                SnapshotRunnerState.BUILDING_SNAPSHOT,
                SnapshotRunnerState.REMOVING,
              ]),
              runnerId: In(readyRunners.map((runner) => runner.id)),
            },
            select: { id: true, state: true },
          })
    if (snapshotRunner) {
      return `snapshot runner ${snapshotRunner.id} is ${snapshotRunner.state}`
    }

    return null
  }

  /**
   * Why a sleeping runner must start, or null. Only work someone is waiting on
   * counts: a delete, stop or archive requested while it sleeps is recorded and
   * carried out at the next wake.
   *
   * Everything here also keeps an awake runner on (findBusyReason), so a wake for
   * it is never followed by a sleep with the work still queued, and another wake.
   */
  private async findWakeReason(): Promise<string | null> {
    const waiting = await this.findSandboxId({
      desiredState: SandboxDesiredState.STARTED,
      state: Not(In([SandboxState.STARTED, ...SETTLED_SANDBOX_STATES])),
    })
    if (waiting) {
      return `sandbox ${waiting} is waiting to start`
    }

    const job = await this.findLiveJob([JobStatus.PENDING], WAKE_JOB_TYPES)
    if (job) {
      return `runner job ${job.id} (${job.type}) is queued`
    }

    return null
  }

  private async findSandboxId(where: FindOptionsWhere<Sandbox>): Promise<string | null> {
    const sandbox = await this.sandboxRepository.findOne({ where, select: { id: true } })
    return sandbox?.id ?? null
  }

  /**
   * A job in one of the given states that still means something: its runner
   * exists and it has moved in the last JOB_STALE_MS.
   */
  private async findLiveJob(statuses: JobStatus[], types?: JobType[]): Promise<Job | null> {
    const runners = await this.runnerRepository.find({ select: { id: true } })
    if (runners.length === 0) {
      return null
    }

    return this.jobRepository.findOne({
      where: {
        status: In(statuses),
        runnerId: In(runners.map((runner) => runner.id)),
        updatedAt: MoreThanOrEqual(new Date(Date.now() - JOB_STALE_MS)),
        ...(types ? { type: In(types) } : {}),
      },
      select: { id: true, type: true },
    })
  }

  private async runnerReportedSince(since: number): Promise<boolean> {
    const threshold = Math.max(since, Date.now() - RUNNER_REPORT_FRESH_MS)
    return this.runnerRepository.exists({
      where: { state: RunnerState.READY, lastChecked: MoreThanOrEqual(new Date(threshold)) },
    })
  }

  private async desiredCountIsZero(): Promise<boolean> {
    try {
      return (await this.ecs.getDesiredCount()) === 0
    } catch (error) {
      this.logger.error(`Could not read the runner service from ECS: ${error}`)
      return false
    }
  }

  /**
   * The power state, recovered from ECS when Redis has none (first run, or
   * Redis was flushed). Call with LOCK_TRANSITION held.
   */
  private async getState(): Promise<RunnerPowerState> {
    return (await this.readStoredState()) ?? (await this.applyEcsState(null, await this.ecs.getDesiredCount()))
  }

  /**
   * getState for callers that do not hold LOCK_TRANSITION. Takes the lock only
   * when the state has to be recovered.
   */
  private async currentState(): Promise<RunnerPowerState> {
    return (await this.readStoredState()) ?? (await this.withTransitionLock(() => this.getState()))
  }

  private async readStoredState(): Promise<RunnerPowerState | null> {
    const stored = await this.redis.get(KEY_STATE)
    if (stored && (Object.values(RunnerPowerState) as string[]).includes(stored)) {
      return stored as RunnerPowerState
    }
    return null
  }

  /**
   * Stores the state ECS implies, given what Redis held (null for nothing), and
   * returns it. Call with LOCK_TRANSITION held.
   */
  private async applyEcsState(stored: RunnerPowerState | null, desiredCount: number): Promise<RunnerPowerState> {
    let actual: RunnerPowerState
    if (desiredCount === 0) {
      actual = RunnerPowerState.ASLEEP
    } else if (stored === RunnerPowerState.AWAKE || stored === RunnerPowerState.WAKING) {
      actual = stored
    } else if (stored === RunnerPowerState.SLEEPING) {
      // A sleep that died before ECS took it: the runner never went off.
      actual = RunnerPowerState.AWAKE
    } else {
      // Asleep as far as Redis knows, or nothing stored, yet ECS wants the task:
      // someone woke it by hand, or Redis was flushed.
      actual = (await this.runnerReportedSince(0)) ? RunnerPowerState.AWAKE : RunnerPowerState.WAKING
    }

    if (actual === stored) {
      return actual
    }

    if (actual === RunnerPowerState.WAKING) {
      await this.redis.set(KEY_WAKE_REQUESTED_AT, String(Date.now()))
      await this.redis.del(KEY_WAKE_TIMEOUT_LOGGED_AT)
    }
    await this.setState(actual)
    this.logger.log(
      stored
        ? `Runner power state corrected from ECS: ${stored} -> ${actual} (desired count ${desiredCount})`
        : `Runner power state recovered from ECS: ${actual}`,
    )
    return actual
  }

  private async withTransitionLock<T>(fn: () => Promise<T>): Promise<T> {
    await this.redisLockProvider.waitForLock(LOCK_TRANSITION, 60, 15_000)
    try {
      return await fn()
    } finally {
      await this.redisLockProvider.unlock(LOCK_TRANSITION)
    }
  }

  private async setState(state: RunnerPowerState): Promise<void> {
    await this.redis.set(KEY_STATE, state)
    await this.redis.set(KEY_STATE_SINCE, String(Date.now()))
  }

  private async getNumber(key: string): Promise<number> {
    const value = Number(await this.redis.get(key))
    return Number.isFinite(value) ? value : 0
  }

  private minutes(
    key: 'runnerPower.idleMinutes' | 'runnerPower.minAwakeMinutes' | 'runnerPower.wakeTimeoutMinutes',
  ): number {
    return this.configService.get(key) * 60_000
  }

  private retryAfterSeconds(): number {
    return this.configService.get('runnerPower.retryAfterSeconds')
  }
}
