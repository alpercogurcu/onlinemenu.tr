import tseslint from "typescript-eslint"
import onlinemenuEslint from "@onlinemenu/config/eslint"

/**
 * pos-core is plain TypeScript with no React and no framework — only
 * `typescript-eslint` is registered (see packages/ui-kit for the React-flavored
 * sibling config).
 *
 * @type {import("eslint").Linter.Config[]}
 */
const config = [
  ...onlinemenuEslint.base,
  ...tseslint.configs.recommended,
]

export default config
