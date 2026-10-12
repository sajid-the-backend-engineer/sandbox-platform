/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Injectable, Logger } from '@nestjs/common'
import axios from 'axios'
import { TypedConfigService } from '../../config/typed-config.service'
import { RunnerPowerState } from '../enums/runner-power-state.enum'
import { RunnerPowerEcsClient } from './runner-power-ecs.client'

const HEALTH_TIMEOUT_MS = 3_000

/**
 * The services that are only useful while a sandbox can run, and so are switched
 * off and on together with the runner: the proxy, which carries traffic into
 * running sandboxes, and the snapshot registry, which the runner pulls images
 * from. Named in RUNNER_POWER_COMPANION_SERVICES; with none named every method
 * here does nothing.
 *
 * They follow the runner, they never lead: RunnerPowerService decides, and this
 * class brings each companion's desired count in line. That is done again on
 * every tick, so a call that failed, or a count someone changed by hand, is put
 * right within 30 seconds. Nothing here throws: a companion that cannot be
 * switched must not stop the runner being switched.
 *
 * A companion at zero sits below its auto-scaling minimum. That is stable:
 * Service Auto Scaling only acts on an alarm, and a service with no tasks reports
 * no metrics for one.
 */
@Injectable()
export class RunnerPowerCompanions {
  private readonly logger = new Logger(RunnerPowerCompanions.name)

  constructor(
    private readonly configService: TypedConfigService,
    private readonly ecs: RunnerPowerEcsClient,
  ) {}

  private names(): string[] {
    return this.configService.get('runnerPower.companionServices') ?? []
  }

  private healthUrls(): string[] {
    return this.configService.get('runnerPower.companionHealthUrls') ?? []
  }

  /** Switch on every companion that is off. */
  async wake(): Promise<void> {
    await this.sync(RunnerPowerState.WAKING)
  }

  /** Switch off every companion that is on, except one in the middle of a deploy. */
  async sleep(): Promise<void> {
    await this.sync(RunnerPowerState.ASLEEP)
  }

  /**
   * Brings the companions in line with the runner's power state. On or coming on:
   * a companion at zero is set to one, and one that is already running is left to
   * its own auto scaling. Off: a companion above zero is set to zero, unless it is
   * being deployed -- a deploy rolls it at one so the new image is started and
   * judged, and that must be allowed to finish. A runner that is going to sleep,
   * or a tick that could not tell (null), changes nothing.
   */
  async sync(state: RunnerPowerState | null): Promise<void> {
    const names = this.names()
    if (names.length === 0) {
      return
    }

    const on = state === RunnerPowerState.AWAKE || state === RunnerPowerState.WAKING
    if (!on && state !== RunnerPowerState.ASLEEP) {
      return
    }

    try {
      const statuses = await this.ecs.describeServices(names)
      for (const name of names) {
        const status = statuses.get(name)
        if (!status) {
          this.logger.warn(`Service ${name} sleeps with the runner but was not found in the cluster`)
          continue
        }

        if (on && status.desiredCount === 0) {
          await this.ecs.setServiceDesiredCount(name, 1)
          this.logger.log(`Switched ${name} on with the runner`)
        } else if (!on && status.desiredCount > 0 && !status.deploying) {
          await this.ecs.setServiceDesiredCount(name, 0)
          this.logger.log(`Switched ${name} off with the runner`)
        }
      }
    } catch (error) {
      this.logger.error(`Could not switch the services that sleep with the runner: ${error}`)
    }
  }

  /**
   * Why a sandbox could not be served yet on account of a companion, or null when
   * all of them answer. Asked once the runner itself has reported, so the runner
   * is not announced as up while the proxy in front of it still refuses.
   *
   * Each health URL is asked the way a client reaches the service, through its
   * load balancer, which answers 503 until a task is registered and healthy. Any
   * answer below 500 counts as up: the registry answers 401 to a caller without
   * credentials. With no URLs configured, a running task is taken as up.
   */
  async notReadyReason(): Promise<string | null> {
    const names = this.names()
    if (names.length === 0) {
      return null
    }

    const urls = this.healthUrls()
    if (urls.length === 0) {
      return this.notRunningReason(names)
    }

    const reasons = await Promise.all(urls.map((url) => this.unhealthyReason(url)))
    return reasons.find((reason) => reason !== null) ?? null
  }

  private async unhealthyReason(url: string): Promise<string | null> {
    try {
      const response = await axios.get(url, {
        timeout: HEALTH_TIMEOUT_MS,
        maxRedirects: 0,
        validateStatus: () => true,
      })
      return response.status >= 500 ? `${url} answers ${response.status}` : null
    } catch (error) {
      return `${url} does not answer (${error instanceof Error ? error.message : error})`
    }
  }

  private async notRunningReason(names: string[]): Promise<string | null> {
    try {
      const statuses = await this.ecs.describeServices(names)
      const notRunning = names.find((name) => (statuses.get(name)?.runningCount ?? 0) === 0)
      return notRunning ? `${notRunning} has no running task` : null
    } catch (error) {
      return `the services that sleep with the runner could not be read (${error})`
    }
  }
}
