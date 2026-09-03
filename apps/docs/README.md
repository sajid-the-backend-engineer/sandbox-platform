<div align="center">

[![Documentation](https://img.shields.io/github/v/release/northrays/docs?label=Docs&color=23cc71)](https://www.northrays.com/docs)
![License](https://img.shields.io/badge/License-AGPL--3-blue)
[![Go Report Card](https://goreportcard.com/badge/github.com/northrays/sandbox-platform)](https://goreportcard.com/report/github.com/northrays/sandbox-platform)
[![Issues - northrays](https://img.shields.io/github/issues/northrays/northrays)](https://github.com/northrays/sandbox-platform/issues)
![GitHub Release](https://img.shields.io/github/v/release/northrays/northrays)

</div>

&nbsp;

<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://github.com/northrays/sandbox-platform/raw/main/assets/images/Northrays-logotype-white.png">
    <source media="(prefers-color-scheme: light)" srcset="https://github.com/northrays/sandbox-platform/raw/main/assets/images/Northrays-logotype-black.png">
    <img alt="Northrays logo" src="https://github.com/northrays/sandbox-platform/raw/main/assets/images/Northrays-logotype-black.png" width="50%">
  </picture>
</div>

<h3 align="center">
  Run AI Code.
  <br/>
  Secure and Elastic Infrastructure for
  Running Your AI-Generated Code.
</h3>

<p align="center">
    <a href="https://www.northrays.com/docs"> Documentation </a>·
    <a href="https://github.com/northrays/sandbox-platform/issues/new?assignees=&labels=bug&projects=&template=bug_report.md&title=%F0%9F%90%9B+Bug+Report%3A+"> Report Bug </a>·
    <a href="https://github.com/northrays/sandbox-platform/issues/new?assignees=&labels=enhancement&projects=&template=feature_request.md&title=%F0%9F%9A%80+Feature%3A+"> Request Feature </a>·
    <a href="https://go.northrays.com/slack"> Join our Slack </a>·
    <a href="https://x.com/northrays"> Connect on X </a>
</p>

<p align="center">
    <a href="https://www.producthunt.com/posts/northrays-2?embed=true&utm_source=badge-top-post-badge&utm_medium=badge&utm_souce=badge-northrays&#0045;2" target="_blank"><img src="https://api.producthunt.com/widgets/embed-image/v1/top-post-badge.svg?post_id=957617&theme=neutral&period=daily&t=1746176740150" alt="Northrays&#0032; - Secure&#0032;and&#0032;elastic&#0032;infra&#0032;for&#0032;running&#0032;your&#0032;AI&#0045;generated&#0032;code&#0046; | Product Hunt" style="width: 250px; height: 54px;" width="250" height="54" /></a>
    <a href="https://www.producthunt.com/posts/northrays-2?embed=true&utm_source=badge-top-post-topic-badge&utm_medium=badge&utm_souce=badge-northrays&#0045;2" target="_blank"><img src="https://api.producthunt.com/widgets/embed-image/v1/top-post-topic-badge.svg?post_id=957617&theme=neutral&period=monthly&topic_id=237&t=1746176740150" alt="Northrays&#0032; - Secure&#0032;and&#0032;elastic&#0032;infra&#0032;for&#0032;running&#0032;your&#0032;AI&#0045;generated&#0032;code&#0046; | Product Hunt" style="width: 250px; height: 54px;" width="250" height="54" /></a>
</p>

---

## Installation

### Python SDK

```bash
pip install northrays
```

### TypeScript SDK

```bash
npm install @northrays/sdk
```

---

## Features

- **Lightning-Fast Infrastructure**: Sub-90ms Sandbox creation from code to execution.
- **Separated & Isolated Runtime**: Execute AI-generated code with zero risk to your infrastructure.
- **Massive Parallelization for Concurrent AI Workflows**: Fork Sandbox filesystem and memory state (Coming soon!)
- **Programmatic Control**: File, Git, LSP, and Execute API
- **Unlimited Persistence**: Your Sandboxes can live forever
- **OCI/Docker Compatibility**: Use any OCI/Docker image to create a Sandbox

---

## Quick Start

1. Create an account at https://app.northrays.com
1. Generate a [new API key](https://app.northrays.com/dashboard/keys)
1. Follow the [Getting Started docs](https://www.northrays.com/docs/getting-started/) to start using the Northrays SDK

## Creating your first Sandbox

### Python SDK

```py
from northrays import Northrays, NorthraysConfig, CreateSandboxBaseParams

# Initialize the Northrays client
northrays = Northrays(NorthraysConfig(api_key="YOUR_API_KEY"))

# Create the Sandbox instance
sandbox = northrays.create(CreateSandboxBaseParams(language="python"))

# Run code securely inside the Sandbox
response = sandbox.process.code_run('print("Sum of 3 and 4 is " + str(3 + 4))')
if response.exit_code != 0:
    print(f"Error running code: {response.exit_code} {response.result}")
else:
    print(response.result)

# Clean up the Sandbox
northrays.delete(sandbox)
```

### Typescript SDK

```jsx
import { Northrays } from '@northrays/sdk'

async function main() {
  // Initialize the Northrays client
  const northrays = new Northrays({
    apiKey: 'YOUR_API_KEY',
  })

  let sandbox
  try {
    // Create the Sandbox instance
    sandbox = await northrays.create({
      language: 'typescript',
    })
    // Run code securely inside the Sandbox
    const response = await sandbox.process.codeRun('console.log("Sum of 3 and 4 is " + (3 + 4))')
    if (response.exitCode !== 0) {
      console.error('Error running code:', response.exitCode, response.result)
    } else {
      console.log(response.result)
    }
  } catch (error) {
    console.error('Sandbox flow error:', error)
  } finally {
    if (sandbox) await northrays.delete(sandbox)
  }
}

main().catch(console.error)
```
