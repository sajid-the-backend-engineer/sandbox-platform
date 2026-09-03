from northrays import Northrays, ListSandboxesQuery, SandboxListSortDirection, SandboxListSortField, SandboxState


def main():
    northrays = Northrays()

    print("Creating sandbox")
    sandbox = northrays.create()
    print("Sandbox created")

    _ = sandbox.set_labels(
        {
            "public": "true",
        }
    )

    print("Stopping sandbox")
    northrays.stop(sandbox)
    print("Sandbox stopped")

    print("Starting sandbox")
    northrays.start(sandbox)
    print("Sandbox started")

    print("Getting existing sandbox")
    existing_sandbox = northrays.get(sandbox.id)
    print("Get existing sandbox")

    response = existing_sandbox.process.exec('echo "Hello World from exec!"', cwd="/home/northrays", timeout=10)
    if response.exit_code != 0:
        print(f"Error: {response.exit_code} {response.result}")
    else:
        print(response.result)

    for sb in northrays.list(
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
    northrays.delete(sandbox)
    print("Sandbox removed")


if __name__ == "__main__":
    main()
