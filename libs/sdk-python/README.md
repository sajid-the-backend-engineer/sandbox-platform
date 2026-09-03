# Northrays Python SDK

The official Python SDK for [Northrays](https://northrays.com), a secure and elastic infrastructure for running AI-generated code. Northrays provides full composable computers — [sandboxes](https://www.northrays.com/docs/en/sandboxes/) — that you can manage programmatically using the Northrays SDK.

The SDK provides an interface for sandbox management, file system operations, Git operations, language server protocol support, process and code execution, and computer use. For more information, see the [documentation](https://www.northrays.com/docs/en/python-sdk/).

## Installation

Install the package using **pip**:

```bash
pip install northrays
```

## Get API key

Generate an API key from the [Northrays Dashboard ↗](https://app.northrays.com/dashboard/keys) to authenticate SDK requests and access Northrays services. For more information, see the [API keys](https://www.northrays.com/docs/en/api-keys/) documentation.

## Configuration

Configure the SDK using [environment variables](https://www.northrays.com/docs/en/configuration/#environment-variables) or by passing a [configuration object](https://www.northrays.com/docs/en/configuration/#configuration-in-code):

- `NORTHRAYS_API_KEY`: Your Northrays [API key](https://www.northrays.com/docs/en/api-keys/)
- `NORTHRAYS_API_URL`: The Northrays [API URL](https://www.northrays.com/docs/en/tools/api/)
- `NORTHRAYS_TARGET`: Your target [region](https://www.northrays.com/docs/en/regions/) environment (e.g. `us`, `eu`)

```python
from northrays import Northrays, NorthraysConfig

# Initialize with environment variables
northrays = Northrays()

# Initialize with configuration object
config = NorthraysConfig(
    api_key="YOUR_API_KEY",
    api_url="YOUR_API_URL",
    target="us"
)
```

## Create a sandbox

Create a sandbox to run your code securely in an isolated environment.

```python
from northrays import Northrays, NorthraysConfig

config = NorthraysConfig(api_key="YOUR_API_KEY")
northrays = Northrays(config)
sandbox = northrays.create()
response = sandbox.process.code_run('print("Hello World")')
```

## Examples and guides

Northrays provides [examples](https://www.northrays.com/docs/en/getting-started/#examples) and [guides](https://www.northrays.com/docs/en/guides/) for common sandbox operations, best practices, and a wide range of topics, from basic usage to advanced topics, showcasing various types of integrations between Northrays and other tools.

### Create a sandbox with custom resources

Create a sandbox with [custom resources](https://www.northrays.com/docs/en/sandboxes/#resources) (CPU, memory, disk).

```python
from northrays import Northrays, CreateSandboxFromImageParams, Image, Resources

northrays = Northrays()
sandbox = northrays.create(
    CreateSandboxFromImageParams(
        image=Image.debian_slim("3.12"),
        resources=Resources(cpu=2, memory=4, disk=8)
    )
)
```

### Create an ephemeral sandbox

Create an [ephemeral sandbox](https://www.northrays.com/docs/en/sandboxes/#ephemeral-sandboxes) that is automatically deleted when stopped.

```python
from northrays import Northrays, CreateSandboxFromSnapshotParams

northrays = Northrays()
sandbox = northrays.create(
    CreateSandboxFromSnapshotParams(ephemeral=True, auto_stop_interval=5)
)
```

### Create a sandbox from a snapshot

Create a sandbox from a [snapshot](https://www.northrays.com/docs/en/snapshots/).

```python
from northrays import Northrays, CreateSandboxFromSnapshotParams

northrays = Northrays()
sandbox = northrays.create(
    CreateSandboxFromSnapshotParams(
        snapshot="my-snapshot-name",
        language="python"
    )
)
```

### Execute Commands

Execute commands in the sandbox.

```python
# Execute a shell command
response = sandbox.process.exec('echo "Hello, World!"')
print(response.result)

# Run Python code
response = sandbox.process.code_run('''
x = 10
y = 20
print(f"Sum: {x + y}")
''')
print(response.result)
```

### File Operations

Upload, download, and search files in the sandbox.

```python
# Upload a file
sandbox.fs.upload_file(b'Hello, World!', 'path/to/file.txt')

# Download a file
content = sandbox.fs.download_file('path/to/file.txt')

# Search for files
matches = sandbox.fs.find_files(root_dir, 'search_pattern')
```

### Git Operations

Clone, list branches, and add files to the sandbox.

```python
# Clone a repository
sandbox.git.clone('https://github.com/example/repo', 'path/to/clone')

# List branches
branches = sandbox.git.branches('path/to/repo')

# Add files
sandbox.git.add('path/to/repo', ['file1.txt', 'file2.txt'])
```

### Language Server Protocol

Create and start a language server to get code completions, document symbols, and more.

```python
# Create and start a language server
lsp = sandbox.create_lsp_server('python', 'path/to/project')
lsp.start()

# Notify the lsp for the file
lsp.did_open('path/to/file.py')

# Get document symbols
symbols = lsp.document_symbols('path/to/file.py')

# Get completions
completions = lsp.completions('path/to/file.py', {"line": 10, "character": 15})
```

Code in [\_sync](./src/northrays/_sync/) directory shouldn't be edited directly. It should be generated from the corresponding async code in the [\_async](./src/northrays/_async/) directory using the SDK generation scripts in the [scripts](./scripts/) directory.
