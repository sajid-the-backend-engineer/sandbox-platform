/*
 * Copyright © 2026 Northrays Private Limited
 * SPDX-License-Identifier: AGPL-3.0
 */
import { MigrationInterface, QueryRunner } from 'typeorm'

export class Migration1782000000000 implements MigrationInterface {
  name = 'Migration1782000000000'

  public async up(queryRunner: QueryRunner): Promise<void> {
    // Defaults to false so every existing sandbox keeps the filter it has. The column
    // only ever widens the seccomp profile, and doing that to workloads that never
    // launch a browser would add kernel surface for nothing.
    await queryRunner.query(`ALTER TABLE "sandbox" ADD "browserSandbox" boolean NOT NULL DEFAULT false`)
  }

  public async down(queryRunner: QueryRunner): Promise<void> {
    await queryRunner.query(`ALTER TABLE "sandbox" DROP COLUMN "browserSandbox"`)
  }
}
