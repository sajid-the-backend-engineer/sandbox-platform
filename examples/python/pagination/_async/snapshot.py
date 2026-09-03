import asyncio

from northrays import AsyncNorthrays


async def main():
    async with AsyncNorthrays() as northrays:
        result = await northrays.snapshot.list(page=2, limit=10)
        for snapshot in result.items:
            print(f"{snapshot.name} ({snapshot.image_name})")


if __name__ == "__main__":
    asyncio.run(main())
