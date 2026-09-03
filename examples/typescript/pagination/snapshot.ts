import { Northrays } from '@northrays/sdk'

async function main() {
  const northrays = new Northrays()

  const result = await northrays.snapshot.list(2, 10)
  console.log(`Found ${result.total} snapshots`)
  result.items.forEach((snapshot) => console.log(`${snapshot.name} (${snapshot.imageName})`))
}

main().catch(console.error)
