FROM ubuntu:24.04

ARG USER_NAME=dev
ARG USER_UID=4000
ARG USER_GID=4000
ARG DOCKER_GID=984
ARG BAZELISK_VERSION=v1.25.0
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

# Claude CLI
RUN npm install -g @anthropic-ai/claude-code

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
        /home/${USER_NAME}/.config/jj \
        /home/${USER_NAME}/.claude \
        /home/${USER_NAME}/.ssh \
        /workspace \
    && chown -R ${USER_UID}:${USER_GID} /home/${USER_NAME} /workspace

USER ${USER_NAME}
WORKDIR /workspace

CMD ["claude", "--dangerously-skip-permissions"]
