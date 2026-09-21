FROM python:3.14.5-slim AS slim

# Update package list and install required dependencies
RUN apt-get update && apt-get install -y \
    curl \
    sudo \
    bash \
    git \
    python3-pip \
    python3-venv \
    ripgrep \
    && rm -rf /var/lib/apt/lists/*

# Change default shell to bash
RUN chsh -s /bin/bash

SHELL ["/bin/bash", "-c"]

# Install pipx and uv
RUN python3 -m pip install --no-cache-dir pipx==1.8.0 && pipx ensurepath && pipx install uv==0.9.26

# Install Python LSP and essential pip packages.
#
# The SDK is deliberately absent. This line read `northrays==0.22.0`, which the
# rebrand renamed from `daytona` without ever publishing under the new name --
# nothing by that name exists on PyPI, so the build failed here every time. It
# is also not needed: the SDK is what talks TO this platform from outside, and
# the runner injects its own daemon binary into the sandbox at start, so nothing
# in here has to import it.
#
# To give sandboxes the SDK anyway, install it from the repository with a build
# arg carrying a read-only token, rather than restoring a name PyPI does not
# serve.
RUN python3 -m pip install --no-cache-dir \
    python-lsp-server==1.14.0 \
    matplotlib==3.10.8 \
    pandas==2.2.3 \
    numpy==2.2.6

# Create the Northrays user and configure sudo access
RUN useradd -m northrays && echo "northrays ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/91-northrays

ENV NVM_DIR=/usr/local/nvm
ENV NODE_VERSION=22.14.0
RUN mkdir -p $NVM_DIR

# Install nvm with node and npm
RUN curl https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash

# install node and npm
RUN source $NVM_DIR/nvm.sh \
    && nvm install $NODE_VERSION \
    && nvm alias default $NODE_VERSION \
    && nvm use default

# add node and npm to path so the commands are available
ENV NODE_PATH=$NVM_DIR/v$NODE_VERSION/lib/node_modules
ENV PATH=$NVM_DIR/versions/node/v$NODE_VERSION/bin:$PATH

# ENV PATH covers normal shells, but a LOGIN shell (bash -l, which npm scripts,
# build tooling and `su -` commonly use) re-derives PATH from /etc/profile and
# drops it, so `node` is silently absent there while working everywhere else.
# This puts it back for login shells too, and the build fails if it does not.
RUN echo "export NVM_DIR=$NVM_DIR" > /etc/profile.d/node.sh && \
    echo "export NODE_PATH=$NVM_DIR/versions/node/v$NODE_VERSION/lib/node_modules" >> /etc/profile.d/node.sh && \
    echo "export PATH=$NVM_DIR/versions/node/v$NODE_VERSION/bin:\$PATH" >> /etc/profile.d/node.sh && \
    chmod 0644 /etc/profile.d/node.sh && \
    bash -lc 'node --version && npm --version'

RUN npm install -g ts-node@10.9.2 typescript@5.9.3 typescript-language-server@5.1.3

# Clients address a sandbox at <home>/workspace, and the daemon takes its
# working directory from the image WORKDIR (os.Getwd() in cmd/daemon/main.go).
# Neither existed: there was no WORKDIR, so the runner fell back to $HOME, and
# no workspace directory was ever created. Every exec that asked for one failed
# with "fork/exec /usr/bin/bash: no such file or directory" -- Go reports a
# missing cmd.Dir against the binary it was about to run, so the message blames
# the shell (which is present at both /bin/bash and /usr/bin/bash) instead of
# the missing directory.
#
# /home/daytona is a compatibility symlink for clients still using the upstream
# home path. It is a filesystem alias, not branding, and can be dropped once
# callers address /home/northrays/workspace.
RUN mkdir -p /home/northrays/workspace && \
    chown -R northrays:northrays /home/northrays && \
    ln -s /home/northrays /home/daytona

WORKDIR /home/northrays/workspace

# Switch to Northrays user
USER northrays

# Create directory for computer use plugin
RUN mkdir -p /usr/local/lib && sudo chown northrays:northrays /usr/local/lib

# Keep the container running indefinitely
ENTRYPOINT [ "sleep", "infinity" ]

# Desktop workloads require a separate snapshot; keep ordinary code execution slim.
FROM slim AS desktop
USER root

# Google Chrome, not Chromium.
#
# browser_runtime.py already probes google-chrome-stable first and falls back to
# chromium, so the backend preferred Chrome all along -- the image simply never had
# it. Chrome carries Widevine and the full proprietary codec set that Chromium does
# not, and those are the two things that make a page render differently in a
# screenshot from how a user sees it.
#
# It comes from Google's own apt repo because it is not in Debian, and unlike
# Chromium it is not freely redistributable. That is acceptable here: this image is
# published to a private registry for our own runners, not shipped to third parties.
# Revisit that if the image is ever distributed more widely.
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl gnupg \
    && curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
       | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" \
       > /etc/apt/sources.list.d/google-chrome.list \
    && rm -rf /var/lib/apt/lists/*

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    google-chrome-stable fonts-liberation \
    libx11-6 libxrandr2 libxext6 libxrender1 libxfixes3 libxss1 libxtst6 libxi6 libxinerama1 \
    libxcb1 libxcb-shm0 libxcb-shape0 libxcb-xfixes0 \
    xvfb xauth x11vnc novnc websockify xfce4 xfce4-terminal dbus-x11 \
    at-spi2-core libatspi2.0-0 ffmpeg procps \
    && rm -rf /var/lib/apt/lists/*
ENV DISPLAY=:0 \
    GTK_MODULES=gail:atk-bridge \
    QT_ACCESSIBILITY=1 \
    NO_AT_BRIDGE=0
USER northrays

# An untargeted build retains the existing slim contract.
FROM slim AS default
