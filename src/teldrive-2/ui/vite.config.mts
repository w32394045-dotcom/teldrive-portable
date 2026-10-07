import path from "node:path";
import fs from "node:fs";
import os from "node:os";
import { readdir, readFile, writeFile } from "node:fs/promises";
import { constants as zlibConstants, brotliCompress } from "node:zlib";
import { promisify } from "node:util";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react, { reactCompilerPreset } from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import Icons from "unplugin-icons/vite";
import { defineConfig } from "vite";
import babel from "@rolldown/plugin-babel";

const pdfJsRoot = path.resolve(import.meta.dirname, "node_modules/pdfjs-dist");
const pdfJsAssetDirectories = ["cmaps", "standard_fonts", "wasm", "iccs"] as const;

async function assetFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map((entry) => {
      const file = path.join(directory, entry.name);
      return entry.isDirectory() ? assetFiles(file) : [file];
    }),
  );
  return files.flat();
}

function pdfJsAssets() {
  return {
    name: "pdfjs-assets",
    enforce: "pre" as const,
    transform(code: string, id: string) {
      if (id.includes("/foliate-js/view.js")) {
        return code.replace(
          `    else if (await isPDF(file)) {
        const { makePDF } = await import('./pdf.js')
        book = await makePDF(file)
    }`,
          `    else if (await isPDF(file)) {
        throw new UnsupportedTypeError('PDF files use the Teldrive PDF reader')
    }`,
        );
      }
      if (id.includes("/foliate-js/paginator.js")) {
        let patched = code.replace(
          "#observer = new ResizeObserver(() => this.expand())",
          "#observer = new ResizeObserver(entries => { if (entries.some(entry => entry.target.isConnected)) this.expand() })",
        );
        patched = patched.replace(
          "#observer = new ResizeObserver(() => this.render())",
          "#observer = new ResizeObserver(entries => { if (entries.some(entry => entry.target.isConnected)) this.render() })",
        );
        patched = patched.replace(
          "    render(layout) {\n        if (!layout) return",
          "    render(layout) {\n        if (!layout || !this.document) return",
        );
        patched = patched.replace(
          "    render() {\n        if (!this.#view) return",
          "    render() {\n        if (!this.#view?.document) return",
        );
        return patched;
      }
    },
    async generateBundle() {
      for (const directoryName of pdfJsAssetDirectories) {
        const directory = path.join(pdfJsRoot, directoryName);
        for (const file of await assetFiles(directory)) {
          this.emitFile({
            type: "asset",
            fileName: `pdfjs/${directoryName}/${path.relative(directory, file).split(path.sep).join("/")}`,
            source: await readFile(file),
          });
        }
      }
    },
  };
}

const brotli = promisify(brotliCompress);

// Formats worth precompressing once at build time. Already-compressed formats
// (png, woff2, ttf, icc) are excluded because brotli would only add bytes.
const precompressExtensions = new Set([".js", ".mjs", ".css", ".html", ".svg", ".json", ".wasm", ".bcmap"]);
const precompressMinBytes = 1024;

/**
 * Emits a `<name>.br` sibling for every compressible build output. Compressing
 * at build time instead of in the server means the binary can serve brotli at
 * the highest quality with no per-request CPU: quality 11 costs ~11s for the
 * entry bundle, which is free here and unacceptable on a request.
 */
function precompressAssets() {
  return {
    name: "teldrive-precompress",
    apply: "build" as const,
    enforce: "post" as const,
    async writeBundle(options: { dir?: string }, bundle: Record<string, { type: string; fileName: string }>) {
      // Iterating on chunking does not need brotli, and quality 11 is expensive.
      if (process.env.TELDRIVE_SKIP_PRECOMPRESS === "1") return;
      const outDir = options.dir ?? path.resolve(import.meta.dirname, "dist");
      const targets = Object.values(bundle).filter(
        (output) => output.type === "asset" || output.type === "chunk",
      );
      const files = targets
        .map((output) => output.fileName)
        .filter((fileName) => precompressExtensions.has(path.extname(fileName).toLowerCase()));

      const started = Date.now();
      let saved = 0;
      let written = 0;
      const queue = [...files];
      const workers = Array.from(
        { length: Math.max(1, Math.min(8, os.availableParallelism?.() ?? 4)) },
        async () => {
          for (let fileName = queue.pop(); fileName; fileName = queue.pop()) {
            const file = path.join(outDir, fileName);
            const source = await readFile(file);
            if (source.byteLength < precompressMinBytes) continue;
            const compressed = await brotli(source, {
              params: {
                [zlibConstants.BROTLI_PARAM_QUALITY]: 11,
                [zlibConstants.BROTLI_PARAM_SIZE_HINT]: source.byteLength,
              },
            });
            // Skip incompressible payloads rather than shipping a bigger file.
            if (compressed.byteLength >= source.byteLength) continue;
            await writeFile(`${file}.br`, compressed);
            saved += source.byteLength - compressed.byteLength;
            written += 1;
          }
        },
      );
      await Promise.all(workers);
      this.info(
        `precompressed ${written} assets with brotli: ${(saved / 1024 / 1024).toFixed(2)} MiB saved in ${Date.now() - started}ms`,
      );
    },
  };
}

/**
 * Dumps per-package module sizes for every emitted chunk when
 * `TELDRIVE_BUNDLE_STATS=1`, so "what is making the entry big" is answered with
 * data instead of guesswork. Output goes to tools/bundle-stats.json (gitignored).
 */
function bundleStats() {
  return {
    name: "teldrive-bundle-stats",
    apply: "build" as const,
    generateBundle(_options: unknown, bundle: Record<string, any>) {
      if (process.env.TELDRIVE_BUNDLE_STATS !== "1") return;
      const packageOf = (id: string) => {
        const normalized = id.split(path.sep).join("/");
        const marker = normalized.lastIndexOf("/node_modules/");
        if (marker === -1) return `(app) ${normalized.replace(/^.*\/ui\/src\//, "src/")}`;
        const rest = normalized.slice(marker + "/node_modules/".length);
        return rest.startsWith("@") ? rest.split("/").slice(0, 2).join("/") : rest.split("/")[0];
      };
      const chunks: Record<string, { size: number; packages: Record<string, number> }> = {};
      for (const output of Object.values(bundle) as any[]) {
        if (output.type !== "chunk") continue;
        const packages: Record<string, number> = {};
        for (const [id, module] of Object.entries<any>(output.modules ?? {})) {
          const key = packageOf(id);
          packages[key] = (packages[key] ?? 0) + (module.renderedLength ?? 0);
        }
        chunks[output.fileName] = { size: output.code.length, packages };
      }
      const byPackage: Record<string, number> = {};
      for (const chunk of Object.values(chunks)) {
        for (const [name, size] of Object.entries(chunk.packages)) {
          byPackage[name] = (byPackage[name] ?? 0) + size;
        }
      }
      const target = path.resolve(import.meta.dirname, "../..", "tools", "bundle-stats.json");
      fs.mkdirSync(path.dirname(target), { recursive: true });
      fs.writeFileSync(target, JSON.stringify({ chunks, byPackage }, null, 2));
      this.info(`bundle stats: ${Object.keys(chunks).length} chunks -> ${target}`);
    },
  };
}

export default defineConfig(() => {  const backendAddress = process.env.TELDRIVE_HTTP_ADDRESS ?? "127.0.0.1:8080";
  const backendHost = backendAddress.startsWith("0.0.0.0:")
    ? `127.0.0.1:${backendAddress.slice("0.0.0.0:".length)}`
    : backendAddress;

  return {
    plugins: [
      pdfJsAssets(),
      precompressAssets(),
      bundleStats(),
      tanstackRouter({
        // Split route components into their own chunks: without this every
        // route (settings, task details, viewers) is parsed on first paint.
        autoCodeSplitting: true,
      }),
      react(),
      babel({ presets: [reactCompilerPreset()] }),
      tailwindcss(),
      Icons({
        compiler: "jsx",
        jsx: "react",
        autoInstall: true,
        iconCustomizer(_1, _2, props) {
          props.width = "1.25rem";
          props.height = "1.25rem";
          props.className = "pointer-events-none";
        },
      }),
    ],
    resolve: {
      dedupe: ["react", "react-dom"],
      alias: {
        "@": path.resolve(import.meta.dirname, "./src"),
      },
    },
    optimizeDeps: {
      exclude: ["foliate-js"],
    },
    server: {
      cors: true,
      proxy: {
        "/api": {
          target: `http://${backendHost}`,
        },
      },
    },
  };
});
