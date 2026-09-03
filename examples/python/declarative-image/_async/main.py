import asyncio
import time

from northrays import (
    AsyncNorthrays,
    CreateSandboxFromImageParams,
    CreateSandboxFromSnapshotParams,
    CreateSnapshotParams,
    Image,
    Resources,
)


async def main():
    northrays = AsyncNorthrays()

    try:
        # Generate unique name for the snapshot to avoid conflicts
        snapshot_name = f"python-example:{int(time.time())}"

        # Create local file with some data and add it to the image
        with open("file_example.txt", "w") as f:
            _ = f.write("Hello, World!")

        # Create a Python image with common data science packages
        image = (
            Image.debian_slim("3.12")
            .pip_install(["numpy", "pandas", "matplotlib", "scipy", "scikit-learn", "jupyter"])
            .run_commands(
                "apt-get update && apt-get install -y git",
                "groupadd -r northrays && useradd -r -g northrays -m northrays",
                "mkdir -p /home/northrays/workspace",
            )
            .dockerfile_commands(["USER northrays"])
            .workdir("/home/northrays/workspace")
            .env({"MY_ENV_VAR": "My Environment Variable"})
            .add_local_file("file_example.txt", "/home/northrays/workspace/file_example.txt")
        )

        # Create the image
        print(f"=== Creating Snapshot: {snapshot_name} ===")
        _ = await northrays.snapshot.create(
            CreateSnapshotParams(
                name=snapshot_name,
                image=image,
                resources=Resources(
                    cpu=1,
                    memory=1,
                    disk=3,
                ),
            ),
            on_logs=print,
        )

        # Create first sandbox using the pre-built image
        print("\n=== Creating Sandbox from Pre-built Image ===")
        sandbox1 = await northrays.create(CreateSandboxFromSnapshotParams(snapshot=snapshot_name))

        try:
            # Verify the first sandbox environment
            print("Verifying sandbox from pre-built image:")
            response = await sandbox1.process.exec("python --version && pip list")
            print("Python environment:")
            print(response.result)

            # Verify the file was added to the image
            response = await sandbox1.process.exec("cat file_example.txt")
            print("File content:")
            print(response.result)
        finally:
            # Clean up first sandbox
            await northrays.delete(sandbox1)

        # Create second sandbox with a new dynamic image
        print("=== Creating Sandbox with Dynamic Image ===")

        # Define a new dynamic image for the second sandbox
        dynamic_image = (
            Image.debian_slim("3.11")
            .pip_install(["pytest", "pytest-cov", "black", "isort", "mypy", "ruff"])
            .run_commands("apt-get update && apt-get install -y git", "mkdir -p /home/northrays/project")
            .workdir("/home/northrays/project")
            .env({"ENV_VAR": "My Environment Variable"})
        )

        # Create sandbox with the dynamic image
        sandbox2 = await northrays.create(
            CreateSandboxFromImageParams(
                image=dynamic_image,
            ),
            timeout=0,
            on_snapshot_create_logs=print,
        )

        try:
            # Verify the second sandbox environment
            print("Verifying sandbox with dynamic image:")
            response = await sandbox2.process.exec("pip list | grep -E 'pytest|black|isort|mypy|ruff'")
            print("Development tools:")
            print(response.result)
        finally:
            # Clean up second sandbox
            await northrays.delete(sandbox2)
    finally:
        await northrays.close()


if __name__ == "__main__":
    asyncio.run(main())
