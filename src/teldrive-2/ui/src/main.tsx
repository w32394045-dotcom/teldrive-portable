import { QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { ThemeProvider } from "next-themes";
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

async function startApp() {
  const rootElement = document.getElementById("root")!;

  if (!rootElement.innerHTML) {
    const root = ReactDOM.createRoot(rootElement);
    root.render(
      <QueryClientProvider client={queryClient}>
        <ThemeProvider attribute="class" defaultTheme="dark" enableSystem={false}>
          <LocaleBoundary>
            <RouterProvider router={router} />
            <Toaster
              position="bottom-right"
              richColors
              closeButton
              theme="dark"
              toastOptions={{
                style: {
                  background: "oklch(0.21 0.008 70 / 0.85)",
                  border: "1px solid oklch(0.95 0.02 70 / 0.1)",
                  backdropFilter: "blur(16px)",
                },
              }}
            />
          </LocaleBoundary>
        </ThemeProvider>
      </QueryClientProvider>,
    );
  }
}

startApp();
