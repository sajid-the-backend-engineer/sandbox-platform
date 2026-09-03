# @northrays/opencode is now @northrays/opencode

> **This package has been renamed.** Please use [`@northrays/opencode`](https://www.npmjs.com/package/@northrays/opencode) instead.

## Migration

Update your OpenCode configuration:

```diff
{
  "$schema": "https://opencode.ai/config.json",
- "plugin": ["@northrays/opencode"]
+ "plugin": ["@northrays/opencode"]
}
```

The plugin is identical — only the package name has changed.

## About @northrays/opencode

An OpenCode plugin that automatically runs all sessions in Northrays sandboxes for isolated, reproducible development environments.

For documentation and setup instructions, see the [@northrays/opencode README](https://www.npmjs.com/package/@northrays/opencode).
