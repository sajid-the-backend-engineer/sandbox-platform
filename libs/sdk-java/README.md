# Northrays Java SDK

The official Java SDK for [Northrays](https://northrays.com), a secure and elastic infrastructure for running AI-generated code. Northrays provides full composable computers — [sandboxes](https://www.northrays.com/docs/en/sandboxes/) — that you can manage programmatically using the Northrays SDK.

The SDK provides an interface for sandbox management, file system operations, Git operations, language server protocol support, process and code execution, and computer use. For more information, see the [documentation](https://www.northrays.com/docs/en/java-sdk/).

## Installation

Add the dependency using **Gradle**:

```kotlin
dependencies {
    implementation("io.northrays:sdk:x.y.z")
}
```

or using **Maven**:

```xml
<dependency>
  <groupId>io.northrays</groupId>
  <artifactId>sdk</artifactId>
  <version>x.y.z</version>
</dependency>
```

## Get API key

Generate an API key from the [Northrays Dashboard ↗](https://app.northrays.com/dashboard/keys) to authenticate SDK requests and access Northrays services. For more information, see the [API keys](https://www.northrays.com/docs/en/api-keys/) documentation.

## Configuration

Configure the SDK using [environment variables](https://www.northrays.com/docs/en/configuration/#environment-variables) or by passing a [configuration object](https://www.northrays.com/docs/en/configuration/#configuration-in-code):

- `NORTHRAYS_API_KEY`: Your Northrays [API key](https://www.northrays.com/docs/en/api-keys/)
- `NORTHRAYS_API_URL`: The Northrays [API URL](https://www.northrays.com/docs/en/tools/api/)
- `NORTHRAYS_TARGET`: Your target [region](https://www.northrays.com/docs/en/regions/) environment (e.g. `us`, `eu`)

```java
import io.northrays.sdk.Northrays;
import io.northrays.sdk.NorthraysConfig;

// Initialize with environment variables
Northrays northrays = new Northrays();

// Initialize with configuration object
NorthraysConfig config = new NorthraysConfig.Builder()
    .apiKey("YOUR_API_KEY")
    .apiUrl("YOUR_API_URL")
    .target("us")
    .build();
Northrays northrays = new Northrays(config);
```

## Create a sandbox

Create a sandbox to run your code securely in an isolated environment.

```java
import io.northrays.sdk.Northrays;
import io.northrays.sdk.Sandbox;
import io.northrays.sdk.model.CreateSandboxFromSnapshotParams;
import io.northrays.sdk.model.ExecuteResponse;

try (Northrays northrays = new Northrays()) {
    CreateSandboxFromSnapshotParams params = new CreateSandboxFromSnapshotParams();
    params.setLanguage("python");
    Sandbox sandbox = northrays.create(params);

    ExecuteResponse response = sandbox.process.codeRun("print('Hello World!')");
    System.out.println(response.getResult());

    sandbox.delete();
}
```

## Examples and guides

Northrays provides [examples](https://www.northrays.com/docs/en/getting-started/#examples) and [guides](https://www.northrays.com/docs/en/guides/) for common sandbox operations, best practices, and a wide range of topics, from basic usage to advanced topics, showcasing various types of integrations between Northrays and other tools.

### Create a sandbox with custom resources

Create a sandbox with [custom resources](https://www.northrays.com/docs/en/sandboxes/#resources) (CPU, memory, disk).

```java
import io.northrays.sdk.Northrays;
import io.northrays.sdk.Image;
import io.northrays.sdk.model.CreateSandboxFromImageParams;
import io.northrays.sdk.model.Resources;

try (Northrays northrays = new Northrays()) {
    CreateSandboxFromImageParams params = new CreateSandboxFromImageParams();
    params.setImage(Image.debianSlim("3.12"));
    params.setResources(new Resources(2, null, 4, 8));
    Sandbox sandbox = northrays.create(params);
}
```

### Create a sandbox from a snapshot

Create a sandbox from a [snapshot](https://www.northrays.com/docs/en/snapshots/).

```java
import io.northrays.sdk.Northrays;
import io.northrays.sdk.model.CreateSandboxFromSnapshotParams;

try (Northrays northrays = new Northrays()) {
    CreateSandboxFromSnapshotParams params = new CreateSandboxFromSnapshotParams();
    params.setSnapshot("my-snapshot-name");
    params.setLanguage("python");
    Sandbox sandbox = northrays.create(params);
}
```

### Execute commands

Execute commands in the sandbox.

```java
// Execute a shell command
ExecuteResponse response = sandbox.process.executeCommand("echo 'Hello, World!'");
System.out.println(response.getResult());

// Run Python code
ExecuteResponse code = sandbox.process.codeRun("print('Sum:', 10 + 20)");
System.out.println(code.getResult());
```

### File operations

Upload, download, and search files in the sandbox.

```java
// Upload a file
sandbox.fs.uploadFile("Hello, World!".getBytes(), "path/to/file.txt");

// Download a file
byte[] content = sandbox.fs.downloadFile("path/to/file.txt");

// Search for files
List<Match> matches = sandbox.fs.searchFiles(rootDir, "search_pattern");
```

### Git operations

Clone, list branches, and get status in the sandbox.

```java
// Clone a repository
sandbox.git.clone("https://github.com/example/repo", "path/to/clone");

// List branches
Map<String, Object> branches = sandbox.git.branches("path/to/repo");

// Get status
GitStatus status = sandbox.git.status("path/to/repo");
```

### Language server protocol

Create and start a language server to get code completions, document symbols, and more.

```java
// Create and start a language server
LspServer lsp = sandbox.createLspServer("typescript", "path/to/project");
lsp.start("typescript", "path/to/project");

// Notify the LSP for a file
lsp.didOpen("typescript", "path/to/project", "path/to/file.ts");

// Get document symbols
List<LspSymbol> symbols = lsp.documentSymbols("typescript", "path/to/project", "path/to/file.ts");

// Get completions
CompletionList completions = lsp.completions("typescript", "path/to/project", "path/to/file.ts", 10, 15);
```
