import { Check, Moon, Sun, SunMoon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { THEMES, useTheme } from "@/lib/theme";

const ICONS = { system: SunMoon, light: Sun, dark: Moon };

/** Chooses light, dark, or whatever the device prefers. */
export function ThemeButton() {
  const [choice, setChoice] = useTheme();
  const Icon = ICONS[choice];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={`Theme: ${choice}`}>
          <Icon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {THEMES.map((theme) => (
          <DropdownMenuItem key={theme.id} onSelect={() => setChoice(theme.id)}>
            <span className="flex-1">{theme.label}</span>
            {theme.id === choice && <Check />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
