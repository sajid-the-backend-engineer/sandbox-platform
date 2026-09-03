import asyncio

from northrays import AsyncNorthrays, ListSandboxesQuery, SandboxListSortDirection, SandboxListSortField, SandboxState


async def main():
    async with AsyncNorthrays() as northrays:
        print("Creating sandbox")
        sandbox = await northrays.create()
        print("Sandbox created")

        _ = await sandbox.set_labels(
            {
                "public": "true",
            }
        )

        print("Stopping sandbox")
        await northrays.stop(sandbox)
        print("Sandbox stopped")

        print("Starting sandbox")
        await northrays.start(sandbox)
        print("Sandbox started")

        print("Getting existing sandbox")
        existing_sandbox = await northrays.get(sandbox.id)
        print("Get existing sandbox")

        response = await existing_sandbox.process.exec('echo "Hello World from exec!"', cwd="/home/northrays", timeout=10)
        if response.exit_code != 0:
            print(f"Error: {response.exit_code} {response.result}")
        else:
            print(response.result)

        async for sb in northrays.list(
            ListSandboxesQuery(
                limit=10,
                labels={"env": "dev"},
                states=[SandboxState.STARTED],
                sort=SandboxListSortField.CREATEDAT,
                order=SandboxListSortDirection.DESC,
            )
        ):
            print(sb.id)

        print("Removing sandbox")
        await northrays.delete(sandbox)
        print("Sandbox removed")


if __name__ == "__main__":
    asyncio.run(main())
