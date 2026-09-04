// Copyright Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

import { app, HttpRequest, HttpResponseInit, InvocationContext } from '@azure/functions'
import { Northrays, Image } from '@northrays/sdk'

export async function sandboxesHandler(_req: HttpRequest, _ctx: InvocationContext): Promise<HttpResponseInit> {
  const image = Image.base('alpine').env({ FOO: 'bar' })
  const northrays = new Northrays({
    apiKey: process.env.NORTHRAYS_API_KEY,
    apiUrl: process.env.NORTHRAYS_API_URL,
  })
  const iter = northrays.list()
  const listOk = typeof (iter as any)[Symbol.asyncIterator] === 'function' && typeof (await iter.next()) === 'object'
  return {
    jsonBody: {
      imageOk: image.dockerfile.includes('FROM alpine'),
      listOk,
    },
  }
}

app.http('sandboxes', { methods: ['GET'], handler: sandboxesHandler })
