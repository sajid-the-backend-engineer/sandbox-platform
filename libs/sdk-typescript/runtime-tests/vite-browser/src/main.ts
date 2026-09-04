// Copyright Daytona Platforms Inc.
// Copyright © 2026 Northrays Private Limited
// SPDX-License-Identifier: Apache-2.0

import { Buffer } from 'buffer'
import { Northrays, Image } from '@northrays/sdk'

const result: Record<string, unknown> = {
  imageOk: false,
  northraysConstructorOk: false,
  fsThrowsOk: false,
  bufferOk: false,
  listOk: false,
  downloadFileOk: false,
}

try {
  const image = Image.base('alpine').env({ FOO: 'bar' })
  result.imageOk = image.dockerfile.includes('FROM alpine') && image.dockerfile.includes('ENV FOO')
} catch {
  result.imageOk = false
}

try {
  new Northrays({ apiKey: 'browser-test', apiUrl: 'http://invalid.example' })
  result.northraysConstructorOk = true
} catch {
  result.northraysConstructorOk = false
}

try {
  Image.fromDockerfile('/nonexistent')
  result.fsThrowsOk = false
} catch (e: unknown) {
  const msg = e instanceof Error ? e.message : String(e)
  result.fsThrowsOk = /not available|require.*unavailable|fs/.test(msg)
}

// Verify the Buffer polyfill (vite-plugin-node-polyfills with globals.Buffer:true)
// works correctly — same import pattern used by the dashboard's FileTreePane.tsx.
try {
  const buf = Buffer.from('hello buffer')
  result.bufferOk = buf instanceof Uint8Array && buf.toString('utf-8') === 'hello buffer'
} catch (e: unknown) {
  result.bufferError = e instanceof Error ? e.message : String(e)
  result.bufferOk = false
}

// Real API tests — require NORTHRAYS_API_KEY / NORTHRAYS_API_URL injected by the
// Node.js orchestrator and a pre-created sandbox with a test file uploaded.
const apiKey = (window as any).__NORTHRAYS_API_KEY__ as string | undefined
const apiUrl = (window as any).__NORTHRAYS_API_URL__ as string | undefined
const sandboxId = (window as any).__TEST_SANDBOX_ID__ as string | undefined
const fileContent = (window as any).__TEST_FILE_CONTENT__ as string | undefined

if (apiKey && apiUrl) {
  const northrays = new Northrays({ apiKey, apiUrl })

  try {
    const iter = northrays.list()
    if (typeof (iter as any)[Symbol.asyncIterator] !== 'function') {
      throw new Error('list() did not return an async iterator')
    }
    const first = await iter.next()
    result.listOk = typeof first === 'object' && first !== null && 'done' in first
  } catch (e: unknown) {
    result.listError = e instanceof Error ? e.message : String(e)
    result.listOk = false
  }

  // downloadFile exercises the exact regression path fixed in Binary.ts:
  // processDownloadFilesResponseWithBuffered → toBuffer → getBufferCtor.
  if (sandboxId && fileContent) {
    try {
      const sandbox = await northrays.get(sandboxId)
      const buf = await sandbox.fs.downloadFile('test.txt')
      result.downloadFileOk = buf.toString('utf-8') === fileContent
    } catch (e: unknown) {
      result.downloadFileError = e instanceof Error ? e.message : String(e)
      result.downloadFileOk = false
    }
  }
}

;(window as any).__runtimeTestResult = result
document.body.setAttribute('data-result', JSON.stringify(result))
console.log('RUNTIME_TEST_RESULT:' + JSON.stringify(result))
