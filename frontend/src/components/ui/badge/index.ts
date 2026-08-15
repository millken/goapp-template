import type { VariantProps } from "class-variance-authority"
import { cva } from "class-variance-authority"

export { default as Badge } from "./Badge.vue"

export const badgeVariants = cva(
  "inline-flex gap-1 items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2",
  {
    variants: {
      variant: {
        default:
          "border-transparent bg-primary text-primary-foreground hover:bg-primary/80",
        secondary:
          "border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80",
        destructive:
          "border-transparent bg-destructive text-destructive-foreground hover:bg-destructive/80",
        outline: "text-foreground",
        // Not upstream. Task statuses need a third semantic colour: destructive is
        // reserved for danger and default is the page's primary action, which leaves
        // nothing for "this finished cleanly". Emerald because alert already made that
        // the project's success colour for the same reason — a deviation pinned by a
        // test beats bg-emerald-600 literals scattered through three pages, which
        // would break the "components use tokens, pages do not hardcode colours" rule.
        success:
          "border-transparent bg-emerald-600 text-white hover:bg-emerald-600/80 dark:bg-emerald-500",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
)

export type BadgeVariants = VariantProps<typeof badgeVariants>
