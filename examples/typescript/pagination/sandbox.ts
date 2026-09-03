import { Northrays, SandboxListSortDirection, SandboxListSortField, SandboxState } from '@northrays/sdk'

async function main() {
  const northrays = new Northrays()

  for await (const sandbox of northrays.list({
    limit: 10,
    labels: { env: 'dev' },
    states: [SandboxState.STARTED],
    sort: SandboxListSortField.CREATED_AT,
    order: SandboxListSortDirection.DESC,
  })) {
    console.log(sandbox.id)
  }
}

main().catch(console.error)
