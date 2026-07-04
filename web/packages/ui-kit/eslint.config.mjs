import tseslint from "typescript-eslint"
import reactPlugin from "eslint-plugin-react"
import onlinemenuEslint from "@onlinemenu/config/eslint"

/**
 * ui-kit henüz Next.js'e bağlı değil (Wails + admin arasında paylaşılacak
 * shadcn wrapper'lar) — bu yüzden `eslint-config-next` yerine yalnızca
 * `typescript-eslint` + `eslint-plugin-react` doğrudan register edilir.
 *
 * @type {import("eslint").Linter.Config[]}
 */
const config = [
  ...onlinemenuEslint.base,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    plugins: { react: reactPlugin },
    rules: {
      ...reactPlugin.configs.recommended.rules,
      "react/react-in-jsx-scope": "off",
      "react/prop-types": "off",
    },
  },
  {
    // lessons-from-b2b #4: hardcoded string yakalama — bkz. apps/admin/eslint.config.mjs
    files: ["**/*.tsx"],
    rules: {
      "react/jsx-no-literals": [
        "warn",
        {
          noStrings: false,
          ignoreProps: true,
          allowedStrings: ["·", "•", "–", "—", "…", "×", "%", "/", ":", "|", "-", "+"],
        },
      ],
    },
  },
]

export default config
