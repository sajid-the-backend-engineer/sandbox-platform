# @northrays/sdk is now @northrays/sdk

> **This package has been renamed.** Please use [`@northrays/sdk`](https://www.npmjs.com/package/@northrays/sdk) instead.

## Migration

Update your dependency:

```bash
npm uninstall @northrays/sdk
npm install @northrays/sdk
```

or with yarn:

```bash
yarn remove @northrays/sdk
yarn add @northrays/sdk
```

Then update your imports:

```diff
- import { Northrays } from '@northrays/sdk'
+ import { Northrays } from '@northrays/sdk'
```

The API is identical — only the package name has changed.

## About @northrays/sdk

The official TypeScript SDK for [Northrays](https://northrays.com), secure and elastic infrastructure for running AI-generated code.

For documentation, examples, and guides, visit [northrays.com/docs](https://www.northrays.com/docs/en/typescript-sdk/).
