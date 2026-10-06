/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { SnapshotRunner } from '../entities/snapshot-runner.entity'
import { RunnerState } from '../enums/runner-state.enum'
import { SnapshotRunnerState } from '../enums/snapshot-runner-state.enum'
import { RunnerNotReadyError } from '../errors/runner-not-ready.error'
import { SnapshotManager } from './snapshot.manager'

// What happens to a queued image change when its runner is not READY, which with
// runner power management is the normal state while the runner sleeps.
describe('SnapshotManager with the runner not ready', () => {
  let powerManaged: boolean
  let runnerState: RunnerState
  let deleteSnapshotRunner: jest.Mock
  let manager: SnapshotManager

  beforeEach(() => {
    powerManaged = true
    runnerState = RunnerState.UNRESPONSIVE
    deleteSnapshotRunner = jest.fn().mockResolvedValue(undefined)
    const unused = {} as never
    manager = new SnapshotManager(
      unused,
      unused,
      { delete: deleteSnapshotRunner } as never,
      unused,
      unused,
      unused,
      { findOne: async (id: string) => ({ id, state: runnerState }) } as never,
      unused,
      unused,
      unused,
      unused,
      unused,
      unused,
      { isEnabled: () => powerManaged } as never,
    )
  })

  const snapshotRunner = (state: SnapshotRunnerState) => ({ id: 'sr-1', runnerId: 'runner-1', state }) as SnapshotRunner

  it('keeps a removal queued for the next wake', async () => {
    await expect(manager.syncRunnerSnapshotState(snapshotRunner(SnapshotRunnerState.REMOVING))).rejects.toBeInstanceOf(
      RunnerNotReadyError,
    )
    expect(deleteSnapshotRunner).not.toHaveBeenCalled()
  })

  it.each([RunnerState.DISABLED, RunnerState.DECOMMISSIONED])(
    'drops a removal, as upstream does, for a %s runner that will not come back',
    async (state) => {
      runnerState = state

      await expect(
        manager.syncRunnerSnapshotState(snapshotRunner(SnapshotRunnerState.REMOVING)),
      ).rejects.toBeInstanceOf(RunnerNotReadyError)
      expect(deleteSnapshotRunner).toHaveBeenCalledWith('sr-1')
    },
  )

  it('drops a removal, as upstream does, when runner power management is off', async () => {
    powerManaged = false

    await expect(manager.syncRunnerSnapshotState(snapshotRunner(SnapshotRunnerState.REMOVING))).rejects.toBeInstanceOf(
      RunnerNotReadyError,
    )
    expect(deleteSnapshotRunner).toHaveBeenCalledWith('sr-1')
  })

  it('still drops a pull, as upstream does', async () => {
    await expect(
      manager.syncRunnerSnapshotState(snapshotRunner(SnapshotRunnerState.PULLING_SNAPSHOT)),
    ).rejects.toBeInstanceOf(RunnerNotReadyError)
    expect(deleteSnapshotRunner).toHaveBeenCalledWith('sr-1')
  })
})
