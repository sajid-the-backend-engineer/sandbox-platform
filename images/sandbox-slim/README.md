# Northrays Sandbox Slim Image

[Dockerfile](./Dockerfile) contains the definition for [northrays/sandbox](https://hub.docker.com/r/northrays/sandbox) slim images which are used as default snapshots in self-hosted environments.

The slim sandbox image contains Python, Node and some popular dependencies including:

- pipx
- uv
- python-lsp-server
- numpy
- pandas
- matplotlib

- ts-node
- typescript
- typescript-language-server

## NOTE

The slim image does not contain dependencies necessary for Northrays's VNC functionality.
Please use the base image for that.
