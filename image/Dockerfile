FROM node:24-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      git \
      ripgrep \
      jq \
      openssh-client \
      curl \
      ca-certificates \
      python3 \
      make \
      g++ \
      sudo \
      util-linux \
      procps \
      vim \
 && rm -rf /var/lib/apt/lists/*

RUN install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/*

# renovate: datasource=npm depName=t3
ARG T3_VERSION=0.0.40
RUN --mount=type=cache,target=/root/.npm \
    npm install -g "t3@${T3_VERSION}" \
 && npm cache clean --force

RUN curl -fsSL -o /tmp/uv-installer.sh https://astral.sh/uv/0.12.9/install.sh \
 && UV_INSTALL_DIR=/usr/local/bin UV_NO_MODIFY_PATH=1 sh /tmp/uv-installer.sh \
 && rm /tmp/uv-installer.sh \
 && uv --version

RUN install -d -m 0755 /etc/codex \
 && printf '%s\n' 'check_for_update_on_startup = false' > /etc/codex/managed_config.toml \
 && chmod 0644 /etc/codex/managed_config.toml

RUN userdel -r node 2>/dev/null || true \
 && useradd -m -u 1000 -s /bin/bash ai-agent \
 && echo 'ai-agent ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ai-agent \
 && chmod 0440 /etc/sudoers.d/ai-agent

RUN install -d -o 1000 -g 1000 -m 0755 \
      /home/ai-agent/.cache \
      /home/ai-agent/.cache/npm \
      /home/ai-agent/.cache/bun \
      /home/ai-agent/.cache/go/mod \
      /home/ai-agent/.cache/go/build \
      /home/ai-agent/.cache/pip

# renovate: datasource=npm depName=playwright
ARG PLAYWRIGHT_VERSION=1.62.1
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
RUN --mount=type=cache,target=/root/.npm \
    npx --yes "playwright@${PLAYWRIGHT_VERSION}" install --with-deps chromium \
 && chmod -R a+rX /opt/ms-playwright \
 && npm cache clean --force

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY vswarm-repos /usr/local/bin/vswarm-repos
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/vswarm-repos

COPY prompt.sh /tmp/prompt.sh
RUN cat /tmp/prompt.sh >> /etc/bash.bashrc && rm /tmp/prompt.sh

USER 1000
ENV T3CODE_HOME=/home/ai-agent/.config/t3
WORKDIR /home/ai-agent/repos

EXPOSE 3773
HEALTHCHECK --interval=15s --timeout=5s --retries=10 --start-period=40s \
  CMD ["curl", "-fsS", "http://127.0.0.1:3773/api/auth/session"]

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
