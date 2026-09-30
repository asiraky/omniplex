import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Slot } from "radix-ui"

import { cn } from "~/lib/utils"

const badgeVariants = cva(
  "inline-flex w-fit shrink-0 items-center justify-center gap-1 overflow-hidden rounded-full border border-transparent px-2 py-0.5 text-xs font-medium whitespace-nowrap transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 [&>svg]:pointer-events-none [&>svg]:size-3",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground [a&]:hover:bg-primary/90",
        secondary:
          "bg-secondary text-secondary-foreground [a&]:hover:bg-secondary/90",
        destructive:
          "bg-destructive text-white focus-visible:ring-destructive/20 dark:bg-destructive/60 dark:focus-visible:ring-destructive/40 [a&]:hover:bg-destructive/90",
        outline:
          "border-border text-foreground [a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
        ghost: "[a&]:hover:bg-accent [a&]:hover:text-accent-foreground",
        link: "text-primary underline-offset-4 [a&]:hover:underline",
        // Coloured by whoever renders it, through `--badge-accent`, so a new
        // harness styles itself without this file learning its name. The text
        // is mixed toward the theme's own foreground rather than used raw:
        // server accents are chosen to glow on a dark background, and at 10px
        // they fall under 4.5:1 on a light one unless they are darkened hard.
        // Light mixes half the foreground in; dark only a little, which is
        // enough to lift a dim accent off the card.
        accent:
          "bg-[color-mix(in_oklch,var(--badge-accent)_14%,transparent)] text-[color-mix(in_oklch,var(--badge-accent)_45%,var(--foreground))] border-[color-mix(in_oklch,var(--badge-accent)_32%,transparent)] dark:bg-[color-mix(in_oklch,var(--badge-accent)_16%,transparent)] dark:text-[color-mix(in_oklch,var(--badge-accent)_85%,var(--foreground))]",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function Badge({
  className,
  variant = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"span"> &
  VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "span"

  return (
    <Comp
      data-slot="badge"
      data-variant={variant}
      className={cn(badgeVariants({ variant }), className)}
      {...props}
    />
  )
}

export { Badge }
