import { Northrays, SandboxListSortDirection, SandboxListSortField, SandboxState } from '@northrays/sdk'

async function main() {
  const northrays = new Northrays()

  console.log('Creating sandbox')
  const sandbox = await northrays.create()
  console.log('Sandbox created')

  await sandbox.setLabels({
    public: 'true',
  })

  console.log('Stopping sandbox')
  await sandbox.stop()
  console.log('Sandbox stopped')

  console.log('Starting sandbox')
  await sandbox.start()
  console.log('Sandbox started')

  console.log('Getting existing sandbox')
  const existingSandbox = await northrays.get(sandbox.id)
  console.log('Got existing sandbox')

  const response = await existingSandbox.process.executeCommand(
    'echo "Hello World from exec!"',
    undefined,
    undefined,
    10,
  )
  if (response.exitCode !== 0) {
    console.error(`Error: ${response.exitCode} ${response.result}`)
  } else {
    console.log(response.result)
  }

  for await (const sb of northrays.list({
    limit: 10,
    labels: { env: 'dev' },
    states: [SandboxState.STARTED],
    sort: SandboxListSortField.CREATED_AT,
    order: SandboxListSortDirection.DESC,
  })) {
    console.log(sb.id)
  }

  console.log('Deleting sandbox')
  await sandbox.delete()
  console.log('Sandbox deleted')
}

main().catch(console.error)
