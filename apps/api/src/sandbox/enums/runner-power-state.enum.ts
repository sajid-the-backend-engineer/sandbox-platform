/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

/**
 * Whether the runner fleet is switched on, as the runner power manager sees it.
 *
 * This is about the EC2 capacity under the runner, not about a runner row's own
 * health: a runner row can be READY or UNRESPONSIVE in any of these states.
 */
export enum RunnerPowerState {
  // The runner service wants its task. Requests pass straight through.
  AWAKE = 'awake',
  // The power manager is between deciding to sleep and asking ECS for zero tasks.
  SLEEPING = 'sleeping',
  // The runner service is at zero tasks. ECS removes the empty host on its own.
  ASLEEP = 'asleep',
  // The runner service has been asked for its task again and the runner has not
  // reported in since.
  WAKING = 'waking',
}
