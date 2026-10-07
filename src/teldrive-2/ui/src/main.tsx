import { QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { ThemeProvider, useTheme } from "next-themes";
import type { ReactNode } from "react";
import ReactDOM from "react-dom/client";
import { Toaster } from "sonner";
import { useLocale } from "./i18n";
import { getQueryClient } from "./lib/queryClient";
import { routeTree } from "./routeTree.gen";
import "./styles/globals.css";

const queryClient = getQueryClient();

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

/**
 * `t()` is a plain function, so components that call it do not subscribe to the
 * locale store. Remounting the tree on a language change is the cheapest way to
 * guarantee every label — including module-level tables read at render time —
 * picks up the new language.
 */
function LocaleBoundary({ children }: { children: ReactNode }) {
  const locale = useLocale();
  return (
    <div key={locale} className="contents">
      {children}
    </div>
  );
}

/**
 * `next-themes` only rewrites the attributes it is told about. The document
 * ships `data-theme="dark"` for a correct first paint, so the theme must be
 * written to both attributes — otherwise the stale `[data-theme="dark"]` token
 * block keeps winning and the light theme silently renders dark.
 */
const THEME_ATTRIBUTES = ["class", "data-theme"] as const;

/** Keeps toast chrome in step with the active theme. */
function AppToaster() {
  const { resolvedTheme } = useTheme();
  const dark = resolvedTheme !== "light";
  return (
    <Toaster
      position="bottom-right"
      richColors
      closeButton
      theme={dark ? "dark" : "light"}
      toastOptions={
        dark
          ? {
              style: {
                background: "oklch(0.21 0.008 70 / 0.85)",
                border: "1px solid oklch(0.95 0.02 70 / 0.1)",
                backdropFilter: "blur(16px)",
              },
            }
          : undefined
      }
    />
  );
}

async function startApp() {
  const rootElement = document.getElementById("root")!;

  if (!rootElement.innerHTML) {
    const root = ReactDOM.createRoot(rootElement);
    root.render(
      <QueryClientProvider client={queryClient}>
        <ThemeProvider attribute={[...THEME_ATTRIBUTES]} defaultTheme="dark" enableSystem={false}>
          <LocaleBoundary>
            <RouterProvider router={router} />
            <AppToaster />
          </LocaleBoundary>
        </ThemeProvider>
      </QueryClientProvider>,
    );
  }
}

startApp();
