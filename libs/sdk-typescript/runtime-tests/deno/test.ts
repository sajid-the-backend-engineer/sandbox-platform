// Copyright Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

import { Northrays, Image } from '@northrays/sdk'

const image = Image.base('alpine').env({ FOO: 'bar' })
if (!image.dockerfile.includes('FROM alpine')) throw new Error('Image.base failed')
if (!image.dockerfile.includes('ENV FOO')) throw new Error('Image.env failed')

const northrays = new Northrays()
const iter = northrays.list()
if (typeof (iter as any)[Symbol.asyncIterator] !== 'function') {
  throw new Error('list() did not return an async iterator')
}
const first = await iter.next()
if (typeof first !== 'object' || !('done' in first)) {
  throw new Error('list() iterator did not yield a valid result')
}
console.log('PASS')
