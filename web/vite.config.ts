import path from 'node:path';
import { execSync } from 'node:child_process';
import babel from '@rolldown/plugin-babel';
import react, { reactCompilerPreset } from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { compression, defineAlgorithm } from 'vite-plugin-compression2';

// git 执行失败时返回空串 (例如从压缩包而非仓库构建), 由调用方各自兜底。
function git(command: string): string {
  try {
    return execSync(command, { stdio: ['ignore', 'pipe', 'ignore'] }).toString().trim();
  } catch {
    return '';
  }
}

// 版本与仓库地址优先取构建环境给的变量, 本地构建则回退到本地 git。
// 后端由 build.sh 用同一条 git describe 注入 conf.Version, 两侧同源于同一个 HEAD,
// 前端版本由此与后端必然一致, 不会再出现"前端 unknown 与后端 dev 不一致"的误报。
const appVersion = process.env.VITE_APP_VERSION || git('git describe --tags --abbrev=0') || 'dev';
// 仓库地址取本地 origin: fork 自建时更新检查与界面展示都指向自己的仓库。
const githubRepo =
  process.env.VITE_GITHUB_REPO ||
  git('git remote get-url origin').replace(/\.git$/, '') ||
  'https://github.com/bestruirui/octopus';

export default defineConfig({
  base: './',
  define: {
    // 用 define 而非令 Vite 读取环境变量: 取值有 git 回退逻辑, 在这里定稿才能保证两侧一致。
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(appVersion),
    'import.meta.env.VITE_GITHUB_REPO': JSON.stringify(githubRepo),
  },
  plugins: [
    react(),
    babel({ presets: [reactCompilerPreset()] }),
    compression({
      algorithms: [defineAlgorithm('gzip', { level: 9 })],
      include: /\.(html|css|js|mjs|json|svg|txt|xml)$/,
      deleteOriginalAssets: true,
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  build: {
    outDir: path.resolve(import.meta.dirname, '../static/out'),
    emptyOutDir: true,
  },
  server: {
    hmr: process.env.DISABLE_HMR !== 'true',
    watch: process.env.DISABLE_HMR === 'true' ? null : {},
    proxy: {
      '/api': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
});
