import asyncio

from northrays import AsyncNorthrays, ListSandboxesQuery, SandboxListSortDirection, SandboxListSortField, SandboxState


async def main():
    async with AsyncNorthrays() as northrays:
        async for sandbox in northrays.list(
            ListSandboxesQuery(
                limit=10,
                labels={"env": "dev"},
                states=[SandboxState.STARTED],
                sort=SandboxListSortField.CREATEDAT,
                order=SandboxListSortDirection.DESC,
            )
        ):
            print(sandbox.id)


if __name__ == "__main__":
    asyncio.run(main())
