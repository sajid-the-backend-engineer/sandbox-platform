/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Logger } from '@nestjs/common'
import Redis from 'ioredis'
import { FindOperator, FindOptionsWhere, Repository } from 'typeorm'
import { TypedConfigService } from '../../config/typed-config.service'
import { RUNNER_ASLEEP_CODE, RunnerAsleepError, RunnerStartingError } from '../../exceptions/runner-starting.exception'
import { RedisLockProvider } from '../common/redis-lock.provider'
import { Job } from '../entities/job.entity'
import { Runner } from '../entities/runner.entity'
import { SnapshotRunner } from '../entities/snapshot-runner.entity'
import { JobStatus } from '../enums/job-status.enum'
import { JobType } from '../enums/job-type.enum'
import { RunnerPowerState } from '../enums/runner-power-state.enum'
import { RunnerState } from '../enums/runner-state.enum'
import { SnapshotRunnerState } from '../enums/snapshot-runner-state.enum'
import { SandboxRepository } from '../repositories/sandbox.repository'
import { RunnerPowerEcsClient } from './runner-power-ecs.client'
import { RunnerPowerService, runnerWakeOptionsFromHeader } from './runner-power.service'

// Just enough of ioredis for the service and RedisLockProvider: GET, SET (with
// EX/NX), DEL, EXISTS.
class FakeRedis {
  readonly values = new Map<string, string>()
  readonly ttls = new Map<string, number>()
  onSet: ((key: string, value: string) => void) | undefined

  async get(key: string): Promise<string | null> {
    return this.values.get(key) ?? null
  }

  async set(key: string, value: string, ...args: (string | number)[]): Promise<'OK' | null> {
    if (args.includes('NX') && this.values.has(key)) {
      return null
    }
    this.values.set(key, value)
    if (args.includes('EX')) {
      this.ttls.set(key, Number(args[args.indexOf('EX') + 1]))
    }
    this.onSet?.(key, value)
    return 'OK'
  }

  async del(key: string): Promise<number> {
    return this.values.delete(key) ? 1 : 0
  }

  async exists(key: string): Promise<number> {
    return this.values.has(key) ? 1 : 0
  }
}

type FakeJob = Pick<Job, 'id' | 'type' | 'status' | 'runnerId' | 'updatedAt'>

// Evaluates the few find operators the service uses on jobs, so the tests can
// state rows rather than mock answers.
const matches = (value: unknown, condition: unknown): boolean => {
  if (condition instanceof FindOperator) {
    switch (condition.type) {
      case 'in':
        return (condition.value as unknown[]).includes(value)
      case 'moreThanOrEqual':
        return (value as Date).getTime() >= (condition.value as Date).getTime()
    }
    throw new Error(`the fake job repository does not support ${condition.type}`)
  }
  return value === condition
}

const MINUTE = 60_000
const HOUR = 60 * MINUTE

describe('RunnerPowerService', () => {
  let redis: FakeRedis
  let desiredCount: number
  let ecs: { setDesiredCount: jest.Mock; getDesiredCount: jest.Mock; isDeploying: jest.Mock }
  let sandboxFindOne: jest.Mock
  let unconverged: jest.Mock
  let runnerExists: jest.Mock
  let runners: { id: string; state?: RunnerState }[]
  let jobs: FakeJob[]
  let snapshotRunnerFindOne: jest.Mock
  let config: Record<string, unknown>
  let service: RunnerPowerService

  const state = () => redis.values.get('runner-power:state')
  // Stores a power state, and puts ECS where that state implies.
  const putState = (value: RunnerPowerState, sinceMsAgo: number) => {
    redis.values.set('runner-power:state', value)
    redis.values.set('runner-power:state-since', String(Date.now() - sinceMsAgo))
    desiredCount = value === RunnerPowerState.ASLEEP ? 0 : 1
  }
  const queueJob = (job: Partial<FakeJob>) =>
    jobs.push({
      id: `job-${jobs.length + 1}`,
      type: JobType.CREATE_SANDBOX,
      status: JobStatus.PENDING,
      runnerId: 'runner-1',
      updatedAt: new Date(),
      ...job,
    })

  beforeEach(() => {
    redis = new FakeRedis()
    desiredCount = 1
    ecs = {
      setDesiredCount: jest.fn(async (count: number) => {
        desiredCount = count
      }),
      getDesiredCount: jest.fn(async () => desiredCount),
      isDeploying: jest.fn(async () => false),
    }
    sandboxFindOne = jest.fn().mockResolvedValue(null)
    unconverged = jest.fn().mockResolvedValue(null)
    runnerExists = jest.fn().mockResolvedValue(false)
    runners = [{ id: 'runner-1' }]
    jobs = []
    snapshotRunnerFindOne = jest.fn().mockResolvedValue(null)
    config = {
      'runnerPower.enabled': true,
      'runnerPower.idleMinutes': 20,
      'runnerPower.minAwakeMinutes': 10,
      'runnerPower.wakeTimeoutMinutes': 10,
      'runnerPower.retryAfterSeconds': 15,
    }

    const queryBuilder = {
      select: () => queryBuilder,
      where: () => queryBuilder,
      andWhere: () => queryBuilder,
      getOne: unconverged,
    }

    service = new RunnerPowerService(
      { get: (key: string) => config[key] } as unknown as TypedConfigService,
      ecs as unknown as RunnerPowerEcsClient,
      new RedisLockProvider(redis as unknown as Redis),
      redis as unknown as Redis,
      { findOne: sandboxFindOne, createQueryBuilder: () => queryBuilder } as unknown as SandboxRepository,
      {
        exists: runnerExists,
        // Honours a state filter; a runner without one is READY.
        find: async (options?: { where?: { state?: RunnerState } }) =>
          runners.filter(
            (runner) => !options?.where?.state || (runner.state ?? RunnerState.READY) === options.where.state,
          ),
      } as unknown as Repository<Runner>,
      {
        findOne: async ({ where }: { where: FindOptionsWhere<FakeJob> }) =>
          jobs.find((job) =>
            Object.entries(where).every(([key, condition]) => matches(job[key as keyof FakeJob], condition)),
          ) ?? null,
      } as unknown as Repository<Job>,
      { findOne: snapshotRunnerFindOne } as unknown as Repository<SnapshotRunner>,
    )
  })

  describe('when switched off', () => {
    it('lets every request through and never calls ECS', async () => {
      config['runnerPower.enabled'] = false
      putState(RunnerPowerState.ASLEEP, MINUTE)

      await expect(service.ensureAwake('sandbox create')).resolves.toBeUndefined()
      await expect(service.ensureAwake('sandbox create', { wake: false })).resolves.toBeUndefined()
      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(ecs.getDesiredCount).not.toHaveBeenCalled()
      expect(redis.values.has('runner-power:last-activity')).toBe(false)
    })
  })

  describe('ensureAwake', () => {
    it('lets the request through when the runner is awake', async () => {
      putState(RunnerPowerState.AWAKE, MINUTE)

      await expect(service.ensureAwake('sandbox create')).resolves.toBeUndefined()
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('wakes a sleeping runner and tells the caller to retry', async () => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)

      const attempt = service.ensureAwake('sandbox create')

      await expect(attempt).rejects.toBeInstanceOf(RunnerStartingError)
      await expect(attempt).rejects.toMatchObject({ retryAfterSeconds: 15, status: 503 })
      expect(ecs.setDesiredCount).toHaveBeenCalledWith(1)
      expect(state()).toBe(RunnerPowerState.WAKING)
    })

    it('records the request as activity', async () => {
      putState(RunnerPowerState.AWAKE, MINUTE)

      await service.ensureAwake('sandbox start')

      expect(Date.now() - Number(redis.values.get('runner-power:last-activity'))).toBeLessThan(MINUTE)
    })

    it('starts one runner when many requests arrive at once', async () => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)

      const results = await Promise.allSettled(Array.from({ length: 10 }, () => service.ensureAwake('sandbox create')))

      expect(results.every((r) => r.status === 'rejected' && r.reason instanceof RunnerStartingError)).toBe(true)
      expect(ecs.setDesiredCount).toHaveBeenCalledTimes(1)
    })

    it('lets the request through once the woken runner has reported in', async () => {
      putState(RunnerPowerState.WAKING, 2 * MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - 2 * MINUTE))
      runnerExists.mockResolvedValue(true)

      await expect(service.ensureAwake('sandbox start')).resolves.toBeUndefined()
      expect(state()).toBe(RunnerPowerState.AWAKE)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('keeps telling the caller to retry while the runner is still starting, without asking ECS again', async () => {
      putState(RunnerPowerState.WAKING, MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - MINUTE))

      await expect(service.ensureAwake('sandbox start')).rejects.toBeInstanceOf(RunnerStartingError)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('lets the request through when the power state cannot be read', async () => {
      ecs.getDesiredCount.mockRejectedValue(new Error('AccessDenied'))

      await expect(service.ensureAwake('sandbox create')).resolves.toBeUndefined()
    })
  })

  describe('ensureAwake with X-Runner-Wake: never', () => {
    const noWake = { wake: false }

    it('lets the request through when the runner is awake, without recording activity', async () => {
      putState(RunnerPowerState.AWAKE, MINUTE)

      await expect(service.ensureAwake('sandbox create', noWake)).resolves.toBeUndefined()
      expect(redis.values.has('runner-power:last-activity')).toBe(false)
    })

    it('answers RUNNER_ASLEEP and leaves a sleeping runner asleep', async () => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)

      const attempt = service.ensureAwake('sandbox create', noWake)

      await expect(attempt).rejects.toBeInstanceOf(RunnerAsleepError)
      await expect(attempt).rejects.toMatchObject({ retryAfterSeconds: 15, status: 503 })
      await expect(attempt).rejects.toHaveProperty('response.code', RUNNER_ASLEEP_CODE)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(state()).toBe(RunnerPowerState.ASLEEP)
      expect(redis.values.has('runner-power:last-activity')).toBe(false)
    })

    it('answers RUNNER_ASLEEP while the runner is still starting', async () => {
      putState(RunnerPowerState.WAKING, MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - MINUTE))

      await expect(service.ensureAwake('sandbox start', noWake)).rejects.toBeInstanceOf(RunnerAsleepError)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('lets the request through once the woken runner has reported in', async () => {
      putState(RunnerPowerState.WAKING, 2 * MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - 2 * MINUTE))
      runnerExists.mockResolvedValue(true)

      await expect(service.ensureAwake('sandbox recover', noWake)).resolves.toBeUndefined()
      expect(state()).toBe(RunnerPowerState.AWAKE)
    })

    it('does not delay a sleep', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      redis.values.set('runner-power:last-activity', String(Date.now() - 25 * MINUTE))

      await service.ensureAwake('sandbox create', noWake)
      await service.handleCheck()

      expect(ecs.setDesiredCount).toHaveBeenCalledWith(0)
      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })
  })

  describe('reading the X-Runner-Wake header', () => {
    it.each([['never'], ['Never'], [' NEVER '], ['never, never'], [['never']]])('%p means do not wake', (value) => {
      expect(runnerWakeOptionsFromHeader(value)).toEqual({ wake: false })
    })

    it.each([[undefined], [''], ['always'], [[]]])('%p means wake', (value) => {
      expect(runnerWakeOptionsFromHeader(value)).toEqual({ wake: true })
    })
  })

  describe('going to sleep', () => {
    it('switches the runner off after the idle period with nothing running', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      redis.values.set('runner-power:last-activity', String(Date.now() - 25 * MINUTE))

      await service.handleCheck()

      expect(ecs.setDesiredCount).toHaveBeenCalledWith(0)
      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })

    it('stays on while a sandbox is running', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      sandboxFindOne.mockResolvedValueOnce({ id: 'sandbox-1' })

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(redis.values.has('runner-power:last-busy')).toBe(true)
    })

    it.each([
      ['a runner job is queued', () => queueJob({})],
      ['a requested change is unfinished', () => unconverged.mockResolvedValue({ id: 'sandbox-2' })],
      [
        'an image is being pulled',
        () => snapshotRunnerFindOne.mockResolvedValue({ id: 'sr-1', state: SnapshotRunnerState.PULLING_SNAPSHOT }),
      ],
    ])('stays on while %s', async (_reason, makeBusy) => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      makeBusy()

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('does not switch off while the runner service is being deployed', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      ecs.isDeploying.mockResolvedValue(true)

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(state()).toBe(RunnerPowerState.AWAKE)
      // The quiet period starts again after the deploy, so the new image runs for a while.
      expect(redis.values.has('runner-power:last-busy')).toBe(true)
    })

    it('does not switch off when the deployment state cannot be read', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      ecs.isDeploying.mockRejectedValue(new Error('throttled'))

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(state()).toBe(RunnerPowerState.AWAKE)
    })

    it('does not stay on for image work queued for a runner that is down', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      runners = [{ id: 'runner-1', state: RunnerState.DECOMMISSIONED }]
      snapshotRunnerFindOne.mockResolvedValue({ id: 'sr-1', state: SnapshotRunnerState.REMOVING })

      await service.handleCheck()

      expect(snapshotRunnerFindOne).not.toHaveBeenCalled()
      expect(ecs.setDesiredCount).toHaveBeenCalledWith(0)
    })

    it.each([
      ['a job whose runner no longer exists', () => queueJob({ runnerId: 'deleted-runner' })],
      ['a job that has not moved for 6 hours', () => queueJob({ updatedAt: new Date(Date.now() - 6 * HOUR - MINUTE) })],
      ['a finished job', () => queueJob({ status: JobStatus.COMPLETED })],
    ])('is not kept on by %s', async (_reason, queue) => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      queue()

      await service.handleCheck()

      expect(ecs.setDesiredCount).toHaveBeenCalledWith(0)
      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })

    it('stays on for a running job that moved within 6 hours', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      queueJob({ status: JobStatus.IN_PROGRESS, updatedAt: new Date(Date.now() - 5 * HOUR) })

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('logs why it stays on, naming an example, at most once an hour', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      sandboxFindOne.mockResolvedValue({ id: 'sandbox-7' })
      const log = jest.spyOn((service as unknown as { logger: Logger }).logger, 'log').mockImplementation()

      await service.handleCheck()
      await service.handleCheck()

      const keptOn = log.mock.calls.filter(([message]) => String(message).startsWith('Runner kept on'))
      expect(keptOn).toEqual([['Runner kept on: sandbox sandbox-7 is running or changing state']])
      expect(redis.ttls.get('runner-power:busy-logged')).toBe(60 * 60)
    })

    it('waits the full idle period after the last request', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      redis.values.set('runner-power:last-activity', String(Date.now() - 5 * MINUTE))

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('stays on for the minimum time after waking', async () => {
      putState(RunnerPowerState.AWAKE, 3 * MINUTE)

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('counts busy time inside the minimum awake period', async () => {
      putState(RunnerPowerState.AWAKE, 3 * MINUTE)
      sandboxFindOne.mockResolvedValueOnce({ id: 'sandbox-1' })

      await service.handleCheck()

      expect(Date.now() - Number(redis.values.get('runner-power:last-busy'))).toBeLessThan(MINUTE)
    })

    it('waits the full idle period after the last sandbox stopped', async () => {
      // Woken 25 minutes ago by a request; its sandbox ran until 8 minutes ago.
      putState(RunnerPowerState.AWAKE, 25 * MINUTE)
      redis.values.set('runner-power:last-activity', String(Date.now() - 25 * MINUTE))
      redis.values.set('runner-power:last-busy', String(Date.now() - 8 * MINUTE))

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('cancels the sleep when a request arrives during it', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      redis.values.set('runner-power:last-activity', String(Date.now() - 25 * MINUTE))
      // A request records its activity just after the sleeper publishes SLEEPING.
      redis.onSet = (key, value) => {
        if (key === 'runner-power:state' && value === RunnerPowerState.SLEEPING) {
          redis.values.set('runner-power:last-activity', String(Date.now()))
        }
      }

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(state()).toBe(RunnerPowerState.AWAKE)
    })

    it('stays awake when ECS refuses the change', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      ecs.setDesiredCount.mockRejectedValue(new Error('throttled'))

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.AWAKE)
    })

    it('stays awake when ECS refuses the change and cannot be read back', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      ecs.setDesiredCount.mockRejectedValue(new Error('throttled'))
      ecs.getDesiredCount.mockResolvedValueOnce(1).mockRejectedValue(new Error('throttled'))

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.AWAKE)
    })

    it('goes to sleep when ECS errors but has taken the change', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      ecs.setDesiredCount.mockImplementation(async (count: number) => {
        desiredCount = count
        throw new Error('socket hang up')
      })

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })
  })

  describe('while asleep or waking', () => {
    it('wakes the runner when a start is waiting in the queue', async () => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)
      sandboxFindOne.mockResolvedValue({ id: 'sandbox-1' })

      await service.handleCheck()

      expect(ecs.setDesiredCount).toHaveBeenCalledWith(1)
      expect(state()).toBe(RunnerPowerState.WAKING)
    })

    it('stays asleep when nothing is waiting', async () => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })

    it.each([
      JobType.CREATE_SANDBOX,
      JobType.START_SANDBOX,
      JobType.RECOVER_SANDBOX,
      JobType.RESIZE_SANDBOX,
      JobType.CREATE_BACKUP,
      JobType.SNAPSHOT_SANDBOX,
      JobType.PULL_SNAPSHOT,
    ])('wakes the runner for a queued %s job', async (type) => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)
      queueJob({ type })

      await service.handleCheck()

      expect(ecs.setDesiredCount).toHaveBeenCalledWith(1)
      expect(state()).toBe(RunnerPowerState.WAKING)
    })

    it.each([
      JobType.STOP_SANDBOX,
      JobType.DESTROY_SANDBOX,
      JobType.PAUSE_SANDBOX,
      JobType.REMOVE_SNAPSHOT,
      JobType.UPDATE_SANDBOX_NETWORK_SETTINGS,
    ])('leaves a queued %s job for the next wake', async (type) => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)
      queueJob({ type })

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })

    it.each([
      ['whose runner no longer exists', { runnerId: 'deleted-runner' }],
      ['that has not moved for 6 hours', { updatedAt: new Date(Date.now() - 7 * HOUR) }],
      ['that is already running', { status: JobStatus.IN_PROGRESS }],
    ])('does not wake for a job %s', async (_reason, job) => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)
      queueJob(job)

      await service.handleCheck()

      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('marks the runner awake once it reports in', async () => {
      putState(RunnerPowerState.WAKING, MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - MINUTE))
      runnerExists.mockResolvedValue(true)

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.AWAKE)
    })

    it('logs a wake timeout and asks ECS again', async () => {
      putState(RunnerPowerState.WAKING, 12 * MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - 12 * MINUTE))

      await service.handleCheck()

      expect(ecs.setDesiredCount).toHaveBeenCalledWith(1)
      expect(redis.values.has('runner-power:wake-timeout-logged-at')).toBe(true)
      expect(state()).toBe(RunnerPowerState.WAKING)
    })
  })

  describe('reconciling with ECS', () => {
    it('records the runner as asleep when Redis says awake but ECS is at zero', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      desiredCount = 0

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.ASLEEP)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      // Requests now wake it rather than land on a runner that is not there.
      await expect(service.ensureAwake('sandbox create')).rejects.toBeInstanceOf(RunnerStartingError)
      expect(ecs.setDesiredCount).toHaveBeenCalledWith(1)
    })

    it('records the runner as asleep when Redis says waking but ECS is at zero', async () => {
      putState(RunnerPowerState.WAKING, 2 * MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - 2 * MINUTE))
      desiredCount = 0

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.ASLEEP)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('records a runner woken by hand as awake once it reports', async () => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)
      desiredCount = 1
      runnerExists.mockResolvedValue(true)

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.AWAKE)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
      await expect(service.ensureAwake('sandbox create')).resolves.toBeUndefined()
    })

    it('records a runner woken by hand as waking until it reports', async () => {
      putState(RunnerPowerState.ASLEEP, 30 * MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - 5 * HOUR))
      redis.values.set('runner-power:wake-timeout-logged-at', String(Date.now() - 5 * HOUR))
      desiredCount = 1

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.WAKING)
      expect(Date.now() - Number(redis.values.get('runner-power:wake-requested-at'))).toBeLessThan(MINUTE)
      expect(redis.values.has('runner-power:wake-timeout-logged-at')).toBe(false)
      expect(ecs.setDesiredCount).not.toHaveBeenCalled()
    })

    it('settles a sleep that died before ECS took it', async () => {
      putState(RunnerPowerState.SLEEPING, 5 * MINUTE)
      desiredCount = 1

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.AWAKE)
    })

    it('settles a sleep that died after ECS took it', async () => {
      putState(RunnerPowerState.SLEEPING, 5 * MINUTE)
      desiredCount = 0

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })

    it('leaves the state alone while a wake or a sleep holds the lock', async () => {
      putState(RunnerPowerState.AWAKE, 60 * MINUTE)
      desiredCount = 0
      redis.values.set('runner-power:transition', '1')

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.AWAKE)
      expect(ecs.getDesiredCount).not.toHaveBeenCalled()
    })

    it('carries on with the stored state when ECS cannot be read', async () => {
      putState(RunnerPowerState.WAKING, MINUTE)
      redis.values.set('runner-power:wake-requested-at', String(Date.now() - MINUTE))
      runnerExists.mockResolvedValue(true)
      ecs.getDesiredCount.mockRejectedValue(new Error('throttled'))

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.AWAKE)
    })
  })

  describe('with no stored state', () => {
    it('reads ASLEEP back from ECS', async () => {
      ecs.getDesiredCount.mockResolvedValue(0)

      await service.handleCheck()

      expect(state()).toBe(RunnerPowerState.ASLEEP)
    })

    it('reads AWAKE back when the runner is reporting', async () => {
      runnerExists.mockResolvedValue(true)

      await expect(service.ensureAwake('sandbox create')).resolves.toBeUndefined()
      expect(state()).toBe(RunnerPowerState.AWAKE)
    })
  })
})
