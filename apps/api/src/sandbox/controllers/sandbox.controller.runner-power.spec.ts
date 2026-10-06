/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { Redis } from 'ioredis'
import { OrganizationAuthContext } from '../../common/interfaces/organization-auth-context.interface'
import { RunnerAsleepError, RunnerStartingError } from '../../exceptions/runner-starting.exception'
import { OrganizationService } from '../../organization/services/organization.service'
import { CreateSandboxSnapshotDto } from '../dto/create-sandbox-snapshot.dto'
import { CreateSandboxDto } from '../dto/create-sandbox.dto'
import { ResizeSandboxDto } from '../dto/resize-sandbox.dto'
import { SandboxState } from '../enums/sandbox-state.enum'
import { RunnerPowerService } from '../services/runner-power.service'
import { RunnerService } from '../services/runner.service'
import { SandboxService } from '../services/sandbox.service'
import { SandboxController } from './sandbox.controller'

// The runner-power gate on each endpoint that queues runner work: it runs first,
// passes the X-Runner-Wake choice through, and stops the request when it throws.
describe('SandboxController runner power gate', () => {
  const authContext = { organization: { id: 'org-1' }, organizationId: 'org-1' } as unknown as OrganizationAuthContext
  const sandbox = { id: 'sandbox-1', state: SandboxState.STARTED }

  let ensureAwake: jest.Mock
  let sandboxService: Record<string, jest.Mock>
  let controller: SandboxController

  beforeEach(() => {
    ensureAwake = jest.fn().mockResolvedValue(undefined)
    sandboxService = {
      createFromSnapshot: jest.fn().mockResolvedValue(sandbox),
      recover: jest.fn().mockResolvedValue(sandbox),
      start: jest.fn().mockResolvedValue(sandbox),
      resize: jest.fn().mockResolvedValue(sandbox),
      createBackup: jest.fn().mockResolvedValue(sandbox),
      createSnapshotFromSandbox: jest.fn().mockResolvedValue(sandbox),
      toSandboxDto: jest.fn(async (value) => value),
    }
    const redis = { duplicate: () => ({ subscribe: jest.fn(), on: jest.fn() }) }

    controller = new SandboxController(
      {} as RunnerService,
      sandboxService as unknown as SandboxService,
      {} as OrganizationService,
      { ensureAwake } as unknown as RunnerPowerService,
      redis as unknown as Redis,
    )
  })

  const endpoints: [string, string, string, (wake: { wake: boolean }) => Promise<unknown>][] = [
    [
      'create',
      'sandbox create',
      'createFromSnapshot',
      (wake) => controller.createSandbox(authContext, {} as CreateSandboxDto, wake),
    ],
    [
      'recover',
      'sandbox recover',
      'recover',
      (wake) => controller.recoverSandbox(authContext, 'sandbox-1', false, wake),
    ],
    [
      'recover with skipStart',
      'sandbox recover',
      'recover',
      (wake) => controller.recoverSandbox(authContext, 'sandbox-1', true, wake),
    ],
    ['start', 'sandbox start', 'start', (wake) => controller.startSandbox(authContext, 'sandbox-1', wake)],
    [
      'resize',
      'sandbox resize',
      'resize',
      (wake) => controller.resizeSandbox(authContext, 'sandbox-1', { cpu: 2 } as ResizeSandboxDto, wake),
    ],
    ['backup', 'sandbox backup', 'createBackup', (wake) => controller.createBackup(authContext, 'sandbox-1', wake)],
    [
      'snapshot',
      'sandbox snapshot',
      'createSnapshotFromSandbox',
      (wake) =>
        controller.createSandboxSnapshot(authContext, 'sandbox-1', { name: 'snap' } as CreateSandboxSnapshotDto, wake),
    ],
  ]

  it.each(endpoints)('%s wakes the runner before queueing work', async (_name, reason, serviceMethod, call) => {
    await call({ wake: true })

    expect(ensureAwake).toHaveBeenCalledWith(reason, { wake: true })
    expect(ensureAwake.mock.invocationCallOrder[0]).toBeLessThan(
      sandboxService[serviceMethod].mock.invocationCallOrder[0],
    )
  })

  it.each(endpoints)('%s passes X-Runner-Wake: never through', async (_name, reason, _serviceMethod, call) => {
    await call({ wake: false })

    expect(ensureAwake).toHaveBeenCalledWith(reason, { wake: false })
  })

  it.each(endpoints)('%s queues nothing while the runner is starting', async (_name, _reason, serviceMethod, call) => {
    ensureAwake.mockRejectedValue(new RunnerStartingError(15))

    await expect(call({ wake: true })).rejects.toBeInstanceOf(RunnerStartingError)
    expect(sandboxService[serviceMethod]).not.toHaveBeenCalled()
  })

  it.each(endpoints)(
    '%s queues nothing when asked not to wake a sleeping runner',
    async (_name, _reason, serviceMethod, call) => {
      ensureAwake.mockRejectedValue(new RunnerAsleepError(15))

      await expect(call({ wake: false })).rejects.toBeInstanceOf(RunnerAsleepError)
      expect(sandboxService[serviceMethod]).not.toHaveBeenCalled()
    },
  )
})
