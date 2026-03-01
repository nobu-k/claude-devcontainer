FROM ubuntu:24.04

ARG USER_NAME=dev
ARG USER_UID=4000
ARG USER_GID=4000
ARG DOCKER_GID=984
ARG BAZELISK_VERSION=v1.28.1
ARG JJ_VERSION=0.38.0
ARG NODE_MAJOR=24

ENV DEBIAN_FRONTEND=noninteractive

# System packages
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        git \
        curl \
        ca-certificates \
        openssh-client \
        jq \
        less \
        gnupg \
        unzip \
        xz-utils \
        python3 \
        tzdata \
    && rm -rf /var/lib/apt/lists/*

# Docker CLI
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
        | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && chmod a+r /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
        https://download.docker.com/linux/ubuntu noble stable" \
        > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

# Node.js via NodeSource
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/* \
    && corepack enable && corepack prepare pnpm@latest --activate

# Bazelisk
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSL -o /usr/local/bin/bazel \
        "https://github.com/bazelbuild/bazelisk/releases/download/${BAZELISK_VERSION}/bazelisk-linux-${ARCH}" \
    && chmod +x /usr/local/bin/bazel

# Jujutsu (jj)
RUN ARCH=$(uname -m) \
    && curl -fsSL -o /tmp/jj.tar.gz \
        "https://github.com/jj-vcs/jj/releases/download/v${JJ_VERSION}/jj-v${JJ_VERSION}-${ARCH}-unknown-linux-musl.tar.gz" \
    && tar -xzf /tmp/jj.tar.gz -C /usr/local/bin ./jj \
    && rm /tmp/jj.tar.gz

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] \
        https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Claude CLI
RUN npm install -g @anthropic-ai/claude-code

# Playwright + headless Chromium for visual inspection
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright
RUN npm install -g playwright && playwright install chromium --with-deps \
    && rm -rf /var/lib/apt/lists/*

# take-screenshot helper
RUN cat <<'SCRIPT' > /usr/local/bin/take-screenshot && chmod +x /usr/local/bin/take-screenshot
#!/bin/bash
FULL_PAGE=false
MEDIA=""
while [[ "$1" == --* ]]; do
  case "$1" in
    --full-page) FULL_PAGE=true; shift ;;
    --media) MEDIA="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done
URL="${1:?Usage: take-screenshot [--full-page] [--media print|screen] <url> <output-path> [width] [height]}"
OUTPUT="${2:?Usage: take-screenshot [--full-page] [--media print|screen] <url> <output-path> [width] [height]}"
WIDTH="${3:-1280}"
HEIGHT="${4:-720}"

node -e "
const { chromium } = require('/usr/lib/node_modules/playwright');
(async () => {
  const [, url, output, width, height, fullPage, media] = process.argv;
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: parseInt(width), height: parseInt(height) } });
  if (media) await page.emulateMedia({ media });
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.screenshot({ path: output, fullPage: fullPage === 'true' });
  await browser.close();
})();
" "$URL" "$OUTPUT" "$WIDTH" "$HEIGHT" "$FULL_PAGE" "$MEDIA"
SCRIPT

# vscode-editor: lightweight client for the host editor proxy
RUN cat <<'SCRIPT' > /usr/local/bin/vscode-editor && chmod +x /usr/local/bin/vscode-editor
#!/usr/bin/env node
'use strict';
const fs = require('fs');
const net = require('net');
const path = require('path');
const crypto = require('crypto');

const SOCKET_PATH = '/tmp/claude-editor/editor.sock';
const SHARED_DIR = '/tmp/claude-editor';

const origFile = process.argv[2];
if (!origFile) {
  console.error('Usage: vscode-editor <file>');
  process.exit(1);
}
const origPath = path.resolve(origFile);

// Copy file to shared directory with a unique name
const ext = path.extname(origPath);
const base = path.basename(origPath, ext);
const uniqueName = `${base}-${crypto.randomBytes(4).toString('hex')}${ext}`;
const sharedPath = path.join(SHARED_DIR, uniqueName);

try {
  if (fs.existsSync(origPath)) {
    fs.copyFileSync(origPath, sharedPath);
  } else {
    fs.writeFileSync(sharedPath, '');
  }
} catch (err) {
  console.error(`Failed to copy file to shared dir: ${err.message}`);
  process.exit(1);
}

function cleanup() {
  try { fs.unlinkSync(sharedPath); } catch {}
}
process.on('SIGINT', () => { cleanup(); process.exit(130); });
process.on('SIGTERM', () => { cleanup(); process.exit(143); });

const conn = net.createConnection(SOCKET_PATH, () => {
  conn.write(uniqueName + '\n');
});

conn.on('error', (err) => {
  console.error(`Editor proxy connection failed: ${err.message}`);
  cleanup();
  process.exit(1);
});

let buf = '';
conn.on('data', (chunk) => {
  buf += chunk.toString();
  if (buf.includes('done\n')) {
    // Copy edited file back
    try {
      fs.copyFileSync(sharedPath, origPath);
    } catch (err) {
      console.error(`Failed to copy edited file back: ${err.message}`);
    }
    cleanup();
    conn.end();
  }
});

conn.on('end', () => {
  cleanup();
});
SCRIPT

# User setup
RUN groupadd --gid ${USER_GID} ${USER_NAME} \
    && useradd --uid ${USER_UID} --gid ${USER_GID} -m ${USER_NAME} \
    && groupadd --gid ${DOCKER_GID} docker-host \
    && usermod -aG docker-host ${USER_NAME}

# Environment
ENV CARGO_HOME=/home/${USER_NAME}/.cargo \
    RUSTUP_HOME=/home/${USER_NAME}/.rustup \
    GOROOT=/home/${USER_NAME}/go \
    GOPATH=/home/${USER_NAME}/gopath \
    GOMODCACHE=/home/${USER_NAME}/gopath/pkg/mod \
    BAZELISK_HOME=/home/${USER_NAME}/.cache/bazelisk

ENV PATH="${CARGO_HOME}/bin:${GOROOT}/bin:${GOPATH}/bin:${PATH}"

# Pre-create mount target directories
RUN mkdir -p \
        /home/${USER_NAME}/.cache/bazelisk \
        /home/${USER_NAME}/.cache/pnpm \
        /home/${USER_NAME}/.cargo \
        /home/${USER_NAME}/.rustup \
        /home/${USER_NAME}/go \
        /home/${USER_NAME}/gopath \
        /home/${USER_NAME}/.npm \
        /home/${USER_NAME}/.config/gh \
        /home/${USER_NAME}/.config/jj \
        /home/${USER_NAME}/.claude \
        /home/${USER_NAME}/.ssh \
    && chown -R ${USER_UID}:${USER_GID} /home/${USER_NAME}

USER ${USER_NAME}

CMD ["claude", "--dangerously-skip-permissions"]
