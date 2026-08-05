import type messages from "./messages/tr.json"

// next-intl key checking: with this augmentation, `t("errors.tpyo")` is a
// compile error rather than a "errors.tpyo" string rendered to a diner.
declare global {
  type IntlMessages = typeof messages
}
