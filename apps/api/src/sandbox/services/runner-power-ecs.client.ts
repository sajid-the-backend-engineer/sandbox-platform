/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Injectable } from '@nestjs/common'
import { DescribeServicesCommand, ECSClient, UpdateServiceCommand } from '@aws-sdk/client-ecs'
import { TypedConfigService } from '../../config/typed-config.service'

export interface EcsServiceStatus {
  desiredCount: number
  runningCount: number
  // A deployment is rolling: more than one is listed, or the primary one has not
  // finished.
  deploying: boolean
}

/**
 * The two ECS calls the runner power manager makes, and nothing else.
 *
 * It changes a SERVICE's desired count and never touches an instance. For the
 * runner, ECS managed scaling then adds or removes the host, which is the
 * supported path: stopping a runner host by hand leaves a task ECS cannot reap and
 * the service stuck at zero runners (the 2026-10-03 outage).
 *
 * The same two calls serve the services that sleep and wake with the runner (see
 * RunnerPowerCompanions).
 */
@Injectable()
export class RunnerPowerEcsClient {
  private client: ECSClient | undefined

  constructor(private readonly configService: TypedConfigService) {}

  async setDesiredCount(desiredCount: number): Promise<void> {
    await this.setServiceDesiredCount(this.runnerService(), desiredCount)
  }

  async getDesiredCount(): Promise<number> {
    return (await this.describeRunner()).desiredCount
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
    return (await this.describeRunner()).deploying
  }

  async setServiceDesiredCount(service: string, desiredCount: number): Promise<void> {
    await this.ecs().send(
      new UpdateServiceCommand({
        cluster: this.configService.getOrThrow('runnerPower.ecsCluster'),
        service,
        desiredCount,
      }),
    )
  }

  /**
   * The state of each named service in the runner's cluster, in one call. A
   * service ECS does not know is left out of the result.
   */
  async describeServices(services: string[]): Promise<Map<string, EcsServiceStatus>> {
    const statuses = new Map<string, EcsServiceStatus>()
    if (services.length === 0) {
      return statuses
    }

    const result = await this.ecs().send(
      new DescribeServicesCommand({
        cluster: this.configService.getOrThrow('runnerPower.ecsCluster'),
        services,
      }),
    )
    for (const service of result.services ?? []) {
      if (!service.serviceName || service.desiredCount === undefined) {
        continue
      }
      const deployments = service.deployments ?? []
      statuses.set(service.serviceName, {
        desiredCount: service.desiredCount,
        runningCount: service.runningCount ?? 0,
        deploying:
          deployments.length > 1 ||
          deployments.some(
            (deployment) => deployment.status === 'PRIMARY' && deployment.rolloutState === 'IN_PROGRESS',
          ),
      })
    }
    return statuses
  }

  private runnerService(): string {
    return this.configService.getOrThrow('runnerPower.ecsService')
  }

  private async describeRunner(): Promise<EcsServiceStatus> {
    const status = (await this.describeServices([this.runnerService()])).get(this.runnerService())
    if (!status) {
      throw new Error(`runner service ${this.runnerService()} not found`)
    }
    return status
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
