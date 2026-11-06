import { useState } from "react";

import { recall, remember } from "./remember";

export const THEMES = [
  { id: "system", label: "System" },
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
] as const;

export type ThemeChoice = (typeof THEMES)[number]["id"];

/** The same key as public/theme.js, which applies the choice before the page paints. */
const THEME_KEY = "dextro.theme";

function loadTheme(): ThemeChoice {
  const saved = recall(THEME_KEY);
  return THEMES.find((theme) => theme.id === saved)?.id ?? "system";
}

function applyTheme(choice: ThemeChoice) {
  const dark = choice === "dark" || (choice === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

export function useTheme(): [ThemeChoice, (choice: ThemeChoice) => void] {
  const [choice, setChoice] = useState<ThemeChoice>(loadTheme);
  return [
    choice,
    (next) => {
      setChoice(next);
      remember(THEME_KEY, next);
      applyTheme(next);
    },
  ];
}
