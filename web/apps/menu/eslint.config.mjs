import nextConfig from "eslint-config-next"
import onlinemenuEslint from "@onlinemenu/config/eslint"

/**
 * apps/menu lint yapılandırması — apps/admin'in deseni, iki farkla:
 *
 * 1. `src/components/ui/**` ignore'u YOK: bu uygulamada vendor shadcn kopyası
 *    yok, primitive'ler `@onlinemenu/ui-kit`'ten geliyor.
 * 2. `react/jsx-no-literals` "error": admin'de baseline ihlalleri yüzünden
 *    `warn` idi; burada baseline sıfır, dolayısıyla kural ilk günden sıkı
 *    tutuluyor (lessons-from-b2b #4 — müşteriye dönük yüzeyde hardcoded
 *    Türkçe metin yasak, her string src/messages/tr.json'dan gelir).
 *
 * @type {import("eslint").Linter.Config[]}
 */
const config = [
  ...onlinemenuEslint.base,
  ...nextConfig,
  {
    files: ["src/**/*.tsx"],
    rules: {
      "react/jsx-no-literals": [
        "error",
        {
          noStrings: false,
          ignoreProps: true,
          allowedStrings: ["·", "•", "–", "—", "…", "×", "%", "/", ":", "|", "-", "+", "₺"],
        },
      ],
    },
  },
]

export default config
