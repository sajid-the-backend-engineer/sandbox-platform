// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

import { Northrays, Image } from '@northrays/sdk'

export async function run() {
  const image = Image.base('alpine').env({ FOO: 'bar' })
  if (!image.dockerfile.includes('FROM alpine')) throw new Error('Image.base failed')

  const northrays = new Northrays()
  const iter = northrays.list()
  if (typeof (iter as any)[Symbol.asyncIterator] !== 'function') {
    throw new Error('list() did not return an async iterator')
  }
  const first = await iter.next()
  if (typeof first !== 'object' || !('done' in first)) {
    throw new Error('list() iterator did not yield a valid result')
  }
  return 'PASS'
}
