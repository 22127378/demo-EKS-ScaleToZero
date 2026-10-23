import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, forward /api to a locally running API (go run ./api) so the same
// relative apiBase works as in the container.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        rewrite: (path) => path.replace(/^\/api/, ""),
      },
    },
  },
});
