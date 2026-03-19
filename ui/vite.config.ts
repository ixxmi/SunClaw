import { loadEnv, defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, ".", "");
  const apiTarget = env.VITE_SUNCLAW_API_TARGET || "http://127.0.0.1:8080";

  return {
    base: command === "build" ? "/admin/" : "/",
    plugins: [vue()],
    server: {
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
        },
      },
    },
  };
});
