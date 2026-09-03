# OpenCode Agent

## Overview

This example runs [OpenCode](https://opencode.ai/), an open source coding agent, inside a Northrays sandbox. You can interact with OpenCode via a web interface to run automations, build apps, and launch web apps or services using [Northrays preview links](https://www.northrays.com/docs/en/preview-and-authentication/#fetching-a-preview-link).

## Features

- **Secure sandbox execution:** The agent operates within a controlled environment, along with code or commands run by the agent.
- **75+ LLM providers:** OpenCode supports over 75 different LLM providers, giving you flexibility in choosing your AI model.
- **Custom Northrays-aware agent:** Preconfigured with a system prompt that understands Northrays sandbox paths and preview links.
- **Preview deployed apps:** Use Northrays preview links to view and interact with your deployed applications.

## Prerequisites

- **Node.js:** Version 18 or higher is required

## Environment Variables

To run this example, you need to set the following environment variable:

- `NORTHRAYS_API_KEY`: Required for access to Northrays sandboxes. Get it from [Northrays Dashboard](https://app.northrays.com/dashboard/keys)

Create a `.env` file in the project directory with this variable.

## Getting Started

### Setup and Run

1. Install dependencies:

   ```bash
   npm install
   ```

2. Run the example:

   ```bash
   npm run start
   ```

## How It Works

When this example is run, the agent follows the following workflow:

1. A new Northrays sandbox is created.
2. OpenCode AI is installed inside the sandbox.
3. A [custom agent](https://opencode.ai/docs/agents/) is configured with Northrays-specific instructions.
4. The [OpenCode web server](https://opencode.ai/docs/cli/#web) starts inside the sandbox.
5. You can interact with the agent through the web interface.
6. When the script is terminated, the sandbox is deleted.

## Example Output

```
Creating sandbox...
Installing OpenCode...
Starting OpenCode web server...
Press Ctrl+C to stop.


             ▄
█▀▀█ █▀▀█ █▀▀█ █▀▀▄ █▀▀▀ █▀▀█ █▀▀█ █▀▀█
█░░█ █░░█ █▀▀▀ █░░█ █░░░ █░░█ █░░█ █▀▀▀
▀▀▀▀ █▀▀▀ ▀▀▀▀ ▀  ▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀ ▀▀▀▀

  Web interface:      https://3000-1e0f775c-c01b-40e7-8c64-062fd3dadd75.proxy.northrays.works/
```

Open the provided URL in your browser to interact with the OpenCode agent and start building applications within the sandbox.

## License

See the main project LICENSE file for details.

## References

- [OpenCode Documentation](https://opencode.ai/docs)
- [OpenCode GitHub Repository](https://github.com/anomalyco/opencode)
- [Northrays Documentation](https://www.northrays.com/docs)
