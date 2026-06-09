// Native switch implementation — @radix-ui/react-switch is not installed.
// Uses a checkbox input styled as a toggle with Tailwind peer utilities.
import * as React from "react"

import { cn } from "@/lib/utils"

interface SwitchProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
  onCheckedChange?: (checked: boolean) => void
}

function Switch({ className, onCheckedChange, onChange, checked, defaultChecked, ...props }: SwitchProps) {
  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    onChange?.(e)
    onCheckedChange?.(e.target.checked)
  }

  return (
    <label className="inline-flex cursor-pointer items-center">
      <input
        type="checkbox"
        className="sr-only peer"
        checked={checked}
        defaultChecked={defaultChecked}
        onChange={handleChange}
        {...props}
      />
      <div
        className={cn(
          "relative h-5 w-9 rounded-full bg-input transition-colors",
          "peer-checked:bg-primary",
          "peer-focus-visible:outline-none peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background",
          "after:absolute after:top-0.5 after:left-0.5 after:size-4 after:rounded-full after:bg-background after:shadow after:transition-transform",
          "peer-checked:after:translate-x-4",
          className,
        )}
      />
    </label>
  )
}

export { Switch }
