/*
 * Copyright Daytona Platforms Inc.
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */

import { ApiSchema, ApiProperty } from '@nestjs/swagger'
import { IsString, IsNotEmpty } from 'class-validator'

@ApiSchema({ name: 'SnapshotManagerCredentials' })
export class SnapshotManagerCredentialsDto {
  @ApiProperty({
    description: 'Snapshot Manager username for the region',
    example: 'northrays',
  })
  @IsString()
  @IsNotEmpty()
  username: string

  @ApiProperty({
    description: 'Snapshot Manager password for the region',
  })
  @IsString()
  @IsNotEmpty()
  password: string

  constructor(params: { username: string; password: string }) {
    this.username = params.username
    this.password = params.password
  }
}
