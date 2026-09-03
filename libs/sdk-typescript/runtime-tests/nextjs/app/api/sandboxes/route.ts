// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

import { Northrays, Image } from '@northrays/sdk'

export const dynamic = 'force-dynamic'

export async function GET() {
  const image = Image.base('alpine').env({ FOO: 'bar' })
  const northrays = new Northrays()
  const iter = northrays.list()
  const listOk = typeof (iter as any)[Symbol.asyncIterator] === 'function' && typeof (await iter.next()) === 'object'
  return Response.json({
    imageOk: image.dockerfile.includes('FROM alpine'),
    listOk,
  })
}
