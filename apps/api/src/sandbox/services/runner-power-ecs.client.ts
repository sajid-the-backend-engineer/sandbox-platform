/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Injectable } from '@nestjs/common'
import { DescribeServicesCommand, ECSClient, UpdateServiceCommand } from '@aws-sdk/client-ecs'
import { TypedConfigService } from '../../config/typed-config.service'

/**
 * The two ECS calls the runner power manager makes, and nothing else.
 *
 * It changes the runner SERVICE's desired count and never touches an instance.
 * ECS managed scaling then adds or removes the host, which is the supported path:
 * stopping a runner host by hand leaves a task ECS cannot reap and the service
 * stuck at zero runners (the 2026-10-03 outage).
 */
@Injectable()
export class RunnerPowerEcsClient {
  private client: ECSClient | undefined

  constructor(private readonly configService: TypedConfigService) {}

  async setDesiredCount(desiredCount: number): Promise<void> {
    await this.ecs().send(
      new UpdateServiceCommand({
        cluster: this.configService.getOrThrow('runnerPower.ecsCluster'),
        service: this.configService.getOrThrow('runnerPower.ecsService'),
        desiredCount,
      }),
    )
  }

  async getDesiredCount(): Promise<number> {
    const result = await this.ecs().send(
      new DescribeServicesCommand({
        cluster: this.configService.getOrThrow('runnerPower.ecsCluster'),
        services: [this.configService.getOrThrow('runnerPower.ecsService')],
      }),
    )
    const service = result.services?.[0]
    if (!service || service.desiredCount === undefined) {
      throw new Error(`runner service ${this.configService.get('runnerPower.ecsService')} not found`)
    }
    return service.desiredCount
  }

  /**
   * Is the runner service in the middle of a deployment?
   *
   * A deploy rolls the runner at desired count 1 so that its circuit breaker and
   * the pipeline's stability wait can judge the new image. Switching the runner
   * off in that window would let the deployment finish at zero tasks, unjudged,
   * and a broken image would first run, with nothing to roll it back, at the next
   * wake.
   */
  async isDeploying(): Promise<boolean> {
    const result = await this.ecs().send(
      new DescribeServicesCommand({
        cluster: this.configService.getOrThrow('runnerPower.ecsCluster'),
        services: [this.configService.getOrThrow('runnerPower.ecsService')],
      }),
    )
    const deployments = result.services?.[0]?.deployments ?? []
    return (
      deployments.length > 1 ||
      deployments.some((deployment) => deployment.status === 'PRIMARY' && deployment.rolloutState === 'IN_PROGRESS')
    )
  }

  // Created on first use, so a deployment with power management off never needs
  // an AWS region or credentials for it.
  private ecs(): ECSClient {
    if (!this.client) {
      this.client = new ECSClient({ region: this.configService.getOrThrow('runnerPower.awsRegion') })
    }
    return this.client
  }
}
