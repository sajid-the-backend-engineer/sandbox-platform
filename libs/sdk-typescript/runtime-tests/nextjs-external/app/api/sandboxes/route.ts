// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

import { Northrays, Image } from '@northrays/sdk'

export const dynamic = 'force-dynamic'

export async function GET() {
  const image = Image.base('alpine').env({ FOO: 'bar' })
  const northrays = new Northrays()
  const r = await northrays.snapshot.list()
  return Response.json({
    imageOk: image.dockerfile.includes('FROM alpine'),
    listOk: Array.isArray(r.items),
  })
}
