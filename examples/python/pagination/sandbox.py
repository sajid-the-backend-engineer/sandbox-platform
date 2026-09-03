from northrays import Northrays, ListSandboxesQuery, SandboxListSortDirection, SandboxListSortField, SandboxState


def main():
    northrays = Northrays()

    for sandbox in northrays.list(
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
    main()
