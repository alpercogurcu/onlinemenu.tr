import nextConfig from "eslint-config-next"
import onlinemenuEslint from "@onlinemenu/config/eslint"

/**
 * Next.js 16'da `next lint` kaldırıldı — proje doğrudan ESLint CLI (`eslint .`)
 * kullanır. `eslint-config-next`'in ana export'u zaten flat config array'i
 * (core-web-vitals + typescript) döndürür; burada `react` plugin'i de bu array
 * üzerinden bir kere register edilir. Aşağıdaki ek kurallar (jsx-no-literals) o
 * kaydı yeniden yapmadan, yalnızca `rules` ekleyerek üzerine biner.
 *
 * @type {import("eslint").Linter.Config[]}
 */
const config = [
  ...onlinemenuEslint.base,
  ...nextConfig,
  {
    ignores: [
      "src/components/ui/**", // shadcn/ui — vendor kodu, proje kuralları uygulanmaz
    ],
  },
  {
    // lessons-from-b2b #4: "i18n ilk günden: hardcoded string'leri yakalayan
    // bir lint aç." Proje next-intl kullanıyor (src/messages/tr.json).
    // `noStrings:false` yalnızca JSX text children'ı işaretler (attribute/prop
    // literalleri değil) — b2b'deki asıl regresyon "hardcoded Türkçe toast/metin"
    // idi. Baseline'da ihlal sayısı yüksek olduğundan `warn` ile başlatıldı;
    // bkz. rapor (kademeli sıkılaştırma planı).
    files: ["src/**/*.tsx"],
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
  {
    // Baseline sıkılaştırması sırasında bulunan, mevcut kodda halihazırda ihlal
    // edilen "error" seviyeli kurallar. Toplu düzeltme YAPILMADI (davranış
    // değişikliği riski) — `warn`'a indirildi. Kademeli sıkılaştırma planı ve
    // etkilenen dosya listesi için rapora bakın.
    rules: {
      // eslint-plugin-react-hooks@7 "recommended" seti bu kuralları error
      // yapıyor; mevcut effect'lerin çoğu bu proje kurulmadan ÖNCE yazıldı.
      "react-hooks/set-state-in-effect": "warn",
      "react-hooks/preserve-manual-memoization": "warn",
    },
  },
]

export default config
