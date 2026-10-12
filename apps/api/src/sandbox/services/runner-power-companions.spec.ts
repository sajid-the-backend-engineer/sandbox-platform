/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import axios from 'axios'
import { TypedConfigService } from '../../config/typed-config.service'
import { RunnerPowerState } from '../enums/runner-power-state.enum'
import { RunnerPowerCompanions } from './runner-power-companions'
import { EcsServiceStatus, RunnerPowerEcsClient } from './runner-power-ecs.client'

jest.mock('axios')

const PROXY = 'northrays-proxy'
const REGISTRY = 'northrays-snapshot-manager'
const PROXY_HEALTH = 'https://proxy.example.com/health'
const REGISTRY_HEALTH = 'https://registry.example.com/v2/'

describe('RunnerPowerCompanions', () => {
  let services: Map<string, EcsServiceStatus>
  let ecs: { describeServices: jest.Mock; setServiceDesiredCount: jest.Mock }
  let config: Record<string, unknown>
  let companions: RunnerPowerCompanions
  const get = axios.get as jest.Mock

  const put = (name: string, status: Partial<EcsServiceStatus>) =>
    services.set(name, { desiredCount: 1, runningCount: 1, deploying: false, ...status })
  const desired = (name: string) => services.get(name)?.desiredCount

  beforeEach(() => {
    services = new Map()
    put(PROXY, {})
    put(REGISTRY, {})
    ecs = {
      describeServices: jest.fn(
        async (names: string[]) => new Map([...services].filter(([name]) => names.includes(name))),
      ),
      setServiceDesiredCount: jest.fn(async (name: string, count: number) => {
        put(name, { ...services.get(name), desiredCount: count })
      }),
    }
    config = {
      'runnerPower.companionServices': [PROXY, REGISTRY],
      'runnerPower.companionHealthUrls': [PROXY_HEALTH, REGISTRY_HEALTH],
    }
    get.mockReset()
    get.mockResolvedValue({ status: 200 })
    companions = new RunnerPowerCompanions(
      { get: (key: string) => config[key] } as unknown as TypedConfigService,
      ecs as unknown as RunnerPowerEcsClient,
    )
  })

  describe('with no companion named', () => {
    beforeEach(() => {
      config['runnerPower.companionServices'] = []
    })

    it('never calls ECS and never probes', async () => {
      await companions.wake()
      await companions.sleep()
      await companions.sync(RunnerPowerState.AWAKE)

      expect(await companions.notReadyReason()).toBeNull()
      expect(ecs.describeServices).not.toHaveBeenCalled()
      expect(ecs.setServiceDesiredCount).not.toHaveBeenCalled()
      expect(get).not.toHaveBeenCalled()
    })
  })

  describe('going to sleep with the runner', () => {
    it('switches every companion off', async () => {
      await companions.sleep()

      expect(desired(PROXY)).toBe(0)
      expect(desired(REGISTRY)).toBe(0)
    })

    it('switches off a companion that auto scaling had grown', async () => {
      put(PROXY, { desiredCount: 3, runningCount: 3 })

      await companions.sleep()

      expect(desired(PROXY)).toBe(0)
    })

    it('leaves a companion that is being deployed on, so the new image is judged', async () => {
      put(PROXY, { deploying: true })

      await companions.sleep()

      expect(desired(PROXY)).toBe(1)
      expect(desired(REGISTRY)).toBe(0)
    })

    it('does not call ECS for a companion that is already off', async () => {
      put(PROXY, { desiredCount: 0, runningCount: 0 })
      put(REGISTRY, { desiredCount: 0, runningCount: 0 })

      await companions.sleep()

      expect(ecs.setServiceDesiredCount).not.toHaveBeenCalled()
    })
  })

  describe('waking with the runner', () => {
    beforeEach(() => {
      put(PROXY, { desiredCount: 0, runningCount: 0 })
      put(REGISTRY, { desiredCount: 0, runningCount: 0 })
    })

    it('switches every companion on', async () => {
      await companions.wake()

      expect(desired(PROXY)).toBe(1)
      expect(desired(REGISTRY)).toBe(1)
    })

    it('leaves a running companion to its own auto scaling', async () => {
      put(PROXY, { desiredCount: 3, runningCount: 3 })

      await companions.wake()

      expect(desired(PROXY)).toBe(3)
      expect(ecs.setServiceDesiredCount).toHaveBeenCalledTimes(1)
      expect(ecs.setServiceDesiredCount).toHaveBeenCalledWith(REGISTRY, 1)
    })
  })

  describe('on every tick', () => {
    it('switches on a companion found off while the runner is awake', async () => {
      put(PROXY, { desiredCount: 0, runningCount: 0 })

      await companions.sync(RunnerPowerState.AWAKE)

      expect(desired(PROXY)).toBe(1)
    })

    it('switches off a companion found on while the runner is asleep', async () => {
      await companions.sync(RunnerPowerState.ASLEEP)

      expect(desired(PROXY)).toBe(0)
      expect(desired(REGISTRY)).toBe(0)
    })

    it.each([RunnerPowerState.SLEEPING, null])('changes nothing while the state is %s', async (state) => {
      put(PROXY, { desiredCount: 0, runningCount: 0 })

      await companions.sync(state)

      expect(ecs.describeServices).not.toHaveBeenCalled()
      expect(ecs.setServiceDesiredCount).not.toHaveBeenCalled()
    })
  })

  describe('when ECS fails', () => {
    it('does not throw, so the runner is still switched', async () => {
      ecs.describeServices.mockRejectedValue(new Error('throttled'))

      await expect(companions.wake()).resolves.toBeUndefined()
      await expect(companions.sleep()).resolves.toBeUndefined()
    })

    it('carries on past a companion ECS does not know', async () => {
      services.delete(PROXY)

      await companions.sleep()

      expect(desired(REGISTRY)).toBe(0)
    })
  })

  describe('being ready', () => {
    it('is ready when every health URL answers', async () => {
      expect(await companions.notReadyReason()).toBeNull()
      expect(get).toHaveBeenCalledTimes(2)
    })

    it('counts an answer that asks for credentials as up', async () => {
      get.mockResolvedValue({ status: 401 })

      expect(await companions.notReadyReason()).toBeNull()
    })

    it('is not ready while a load balancer has no healthy task behind it', async () => {
      get.mockImplementation(async (url: string) => ({ status: url === PROXY_HEALTH ? 503 : 200 }))

      expect(await companions.notReadyReason()).toBe(`${PROXY_HEALTH} answers 503`)
    })

    it('is not ready while a health URL does not answer at all', async () => {
      get.mockImplementation(async (url: string) => {
        if (url === REGISTRY_HEALTH) {
          throw new Error('timeout of 3000ms exceeded')
        }
        return { status: 200 }
      })

      expect(await companions.notReadyReason()).toBe(`${REGISTRY_HEALTH} does not answer (timeout of 3000ms exceeded)`)
    })

    describe('with no health URL configured', () => {
      beforeEach(() => {
        config['runnerPower.companionHealthUrls'] = []
      })

      it('takes a running task as up', async () => {
        expect(await companions.notReadyReason()).toBeNull()
        expect(get).not.toHaveBeenCalled()
      })

      it('is not ready while a companion has no running task', async () => {
        put(REGISTRY, { runningCount: 0 })

        expect(await companions.notReadyReason()).toBe(`${REGISTRY} has no running task`)
      })

      it('is not ready when ECS cannot be read', async () => {
        ecs.describeServices.mockRejectedValue(new Error('throttled'))

        expect(await companions.notReadyReason()).toContain('could not be read')
      })
    })
  })
})
