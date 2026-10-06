/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { NotFoundException } from '@nestjs/common'
import { RunnerStartingError } from '../../exceptions/runner-starting.exception'
import { OrganizationService } from '../../organization/services/organization.service'
import { RunnerPowerService } from '../../sandbox/services/runner-power.service'
import { SandboxService } from '../../sandbox/services/sandbox.service'
import { AdminSandboxController } from './sandbox.controller'

describe('AdminSandboxController runner power gate', () => {
  let ensureAwake: jest.Mock
  let findBySandboxId: jest.Mock
  let recover: jest.Mock
  let controller: AdminSandboxController

  beforeEach(() => {
    ensureAwake = jest.fn().mockResolvedValue(undefined)
    findBySandboxId = jest.fn().mockResolvedValue({ id: 'org-1' })
    recover = jest.fn().mockResolvedValue({ id: 'sandbox-1' })
    controller = new AdminSandboxController(
      { recover, toSandboxDto: async (value: unknown) => value } as unknown as SandboxService,
      { findBySandboxId } as unknown as OrganizationService,
      { ensureAwake } as unknown as RunnerPowerService,
    )
  })

  it('wakes the runner before recovering', async () => {
    await controller.recoverSandbox('sandbox-1', { wake: false })

    expect(ensureAwake).toHaveBeenCalledWith('admin sandbox recover', { wake: false })
    expect(ensureAwake.mock.invocationCallOrder[0]).toBeLessThan(recover.mock.invocationCallOrder[0])
  })

  it('recovers nothing while the runner is starting', async () => {
    ensureAwake.mockRejectedValue(new RunnerStartingError(15))

    await expect(controller.recoverSandbox('sandbox-1', { wake: true })).rejects.toBeInstanceOf(RunnerStartingError)
    expect(recover).not.toHaveBeenCalled()
  })

  it('does not wake the runner for a sandbox that does not exist', async () => {
    findBySandboxId.mockResolvedValue(null)

    await expect(controller.recoverSandbox('missing', { wake: true })).rejects.toBeInstanceOf(NotFoundException)
    expect(ensureAwake).not.toHaveBeenCalled()
  })
})
