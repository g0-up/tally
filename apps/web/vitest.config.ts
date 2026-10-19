import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config.ts";

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: "jsdom",
      globals: true,
      setupFiles: ["./src/test/setup.ts"],
      include: ["src/**/*.test.{ts,tsx}", "eslint/**/*.test.js"],
      restoreMocks: true,
      // Route tests load lazy pages on their first visit; a cold transform can take several
      // seconds, so the per-test budget sits above every waitFor/findBy timeout the tests use.
      testTimeout: 15_000,
    },
  }),
);
