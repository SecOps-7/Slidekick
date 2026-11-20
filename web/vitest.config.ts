import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "happy-dom",
    include: ["runtime/**/*.test.ts", "src/**/*.test.ts"],
  },
});
