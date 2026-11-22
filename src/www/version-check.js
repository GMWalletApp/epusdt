(function () {
  "use strict";

  var linkAttribute = "data-epusdt-update-check";
  var dialogId = "epusdt-update-dialog";
  var requestController = null;
  var timeoutId = null;
  var requestInFlight = false;
  var requestGeneration = 0;
  var updatePollTimer = null;
  var backgroundCheckTimer = null;
  var backgroundCheckInFlight = false;
  var updateCapability = null;
  var versionContainer = null;
  var latestHasUpdate = null;
  var latestVersion = "";
  var updateDeadline = 0;
  var updateWasRunning = false;
  var updatePollErrors = 0;

  function tokenFromCookie() {
    var row = document.cookie.split("; ").find(function (item) {
      return item.indexOf("access_token=") === 0;
    });
    if (!row) return "";
    try {
      var value = decodeURIComponent(row.slice("access_token=".length));
      var parsed = JSON.parse(value);
      return typeof parsed === "string" ? parsed : "";
    } catch (_) {
      return "";
    }
  }

  function labels() {
    var localeCookie = document.cookie.split("; ").find(function (item) {
      return item.indexOf("PARAGLIDE_LOCALE=") === 0;
    });
    var lang = (document.documentElement.lang || navigator.language || "").toLowerCase();
    if (localeCookie) {
      try {
        lang = decodeURIComponent(localeCookie.slice("PARAGLIDE_LOCALE=".length)).toLowerCase();
      } catch (_) {}
    }
    return lang.indexOf("zh") === 0
      ? {
          button: "检测更新",
          update: "更新",
          title: "GMPay 更新",
          checking: "检测中...",
          current: "当前运行版本",
          latest: "最新稳定版",
          published: "发布时间",
          notes: "更新说明",
          latestState: "当前已是最新版",
          updateState: "发现新版本",
          noRelease: "尚无稳定 Release",
          timeout: "网络超时，请稍后重试",
          rate: "GitHub 请求受到限流，请稍后重试",
          failed: "检测失败，请稍后重试",
          upgrade: "立即更新",
          upgrading: "正在更新...",
          updated: "更新成功，已确认运行版本",
          updatedManual: "更新成功，请手动重启服务",
          upgradeUnsupported: "当前部署方式不支持一键更新",
          updateTimeout: "更新超时，请检查服务状态后重试",
          verifyingVersion: "服务已恢复，正在确认运行版本...",
          reconnecting: "正在重新连接服务，请稍候...",
          stages: { checking: "正在检查更新", downloading: "正在下载更新", verifying: "正在校验更新", extracting: "正在解压更新", swapping: "正在安装更新", restarting: "正在重启服务" },
          retry: "重试",
          close: "关闭",
          unknown: "未知",
        }
      : {
          button: "Check for updates",
          update: "Update",
          title: "GMPay Update",
          checking: "Checking...",
          current: "Running version",
          latest: "Latest stable",
          published: "Published",
          notes: "Release notes",
          latestState: "You are running the latest version",
          updateState: "A new version is available",
          noRelease: "No stable Release is available",
          timeout: "The network request timed out. Try again later.",
          rate: "GitHub rate limit reached. Try again later.",
          failed: "The update check failed. Try again later.",
          upgrade: "Update now",
          upgrading: "Updating...",
          updated: "Update succeeded. Running version is confirmed.",
          updatedManual: "Update succeeded. Restart the service manually.",
          upgradeUnsupported: "One-click update is not supported by this deployment",
          updateTimeout: "The update timed out. Check the service status and retry.",
          verifyingVersion: "Service is back. Confirming the running version...",
          reconnecting: "Reconnecting to the service. Please wait...",
          stages: { checking: "Checking for updates", downloading: "Downloading update", verifying: "Verifying update", extracting: "Extracting update", swapping: "Installing update", restarting: "Restarting service" },
          retry: "Retry",
          close: "Close",
          unknown: "Unknown",
        };
  }

  function addStyles() {
    if (document.getElementById("epusdt-update-style")) return;
    var style = document.createElement("style");
    style.id = "epusdt-update-style";
    style.textContent =
      "#" + dialogId + "{box-sizing:border-box;border:1px solid var(--border,#e5e7eb);border-radius:.75rem;background:var(--background,#fff);color:var(--foreground,#111827);padding:0;width:calc(100vw - 1rem);max-width:32rem;max-height:calc(100vh - 1rem);max-height:calc(100dvh - 1rem);margin:auto;overflow:hidden;box-shadow:0 25px 50px -12px rgb(0 0 0/.25)}" +
      "#" + dialogId + "::backdrop{background:rgb(0 0 0/.55)}" +
      "#" + dialogId + " .epusdt-update-head{position:relative;display:flex;align-items:center;justify-content:center;gap:.75rem;flex-wrap:wrap;border-bottom:1px solid var(--border,#e5e7eb);padding:1rem 5rem 1rem 1.25rem;text-align:center}" +
      "#" + dialogId + " h2{font-size:1rem;font-weight:600;margin:0;overflow-wrap:anywhere}" +
      "#" + dialogId + " [data-close]{position:absolute;top:.55rem;right:.75rem;margin:0}" +
      "#" + dialogId + " .epusdt-update-body{box-sizing:border-box;max-height:calc(100vh - 9rem);max-height:calc(100dvh - 9rem);overflow-y:auto;padding:1rem 1.25rem;font-size:.875rem;line-height:1.5}" +
      "#" + dialogId + " .epusdt-update-state{border:1px solid var(--border,#e5e7eb);border-radius:.5rem;padding:.65rem .75rem;margin-bottom:.85rem}" +
      "#" + dialogId + " .epusdt-update-state.success{border-color:#10b98199;color:#059669}" +
      "#" + dialogId + " .epusdt-update-state.warning{display:flex;align-items:center;gap:.7rem;min-height:2.75rem;border-color:#d97706;border-left-width:3px;background:rgb(245 158 11/.08);color:var(--foreground,#111827);padding:.7rem .85rem;font-weight:600}" +
      "#" + dialogId + " .epusdt-update-state.warning::before{content:\"\";display:block;flex:none;width:.55rem;height:.55rem;border-radius:50%;background:#d97706;box-shadow:0 0 0 .22rem rgb(217 119 6/.16)}" +
      "#" + dialogId + " .epusdt-update-state.restarting{border-color:#0f766e55;background:rgb(15 118 110/.06);color:var(--foreground,#111827)}" +
      "#" + dialogId + " .epusdt-update-state.restarting .epusdt-update-spinner{color:#0f766e}" +
      "#" + dialogId + " .epusdt-update-state.error{border-color:#ef444499;color:var(--destructive,#dc2626)}" +
      "#" + dialogId + " dl{display:grid;grid-template-columns:minmax(0,auto) minmax(0,1fr);gap:.35rem .75rem;margin:0 0 .85rem;color:var(--muted-foreground,#6b7280)}" +
      "#" + dialogId + " dt,#" + dialogId + " dd{min-width:0;overflow-wrap:anywhere}" +
      "#" + dialogId + " dd{margin:0;color:var(--foreground,#111827)}" +
      "#" + dialogId + " .epusdt-version-value,#" + dialogId + " .epusdt-version-link{box-sizing:border-box;display:inline-flex;align-items:center;min-height:2.25rem;border:1px solid var(--border,#d1d5db);border-radius:.375rem;background:transparent;color:inherit;padding:.4rem .7rem;font:inherit;font-size:.8125rem;text-decoration:none}" +
      "#" + dialogId + " .epusdt-version-link{cursor:pointer}" +
      "#" + dialogId + " .epusdt-version-link:hover{background:var(--muted,#f3f4f6)}" +
      "#" + dialogId + " pre{white-space:pre-wrap;overflow:auto;max-height:10rem;border:1px solid var(--border,#e5e7eb);border-radius:.5rem;background:var(--muted,#f3f4f6);padding:.75rem;font:inherit;font-size:.75rem;color:var(--muted-foreground,#6b7280)}" +
      "#" + dialogId + " .epusdt-update-foot{display:flex;justify-content:flex-end;gap:.5rem;flex-wrap:wrap;border-top:1px solid var(--border,#e5e7eb);padding:.75rem 1.25rem}" +
      "#" + dialogId + " button,#" + dialogId + " a{box-sizing:border-box;min-height:2.25rem;border:1px solid var(--border,#d1d5db);border-radius:.375rem;background:transparent;color:inherit;padding:.4rem .7rem;font:inherit;font-size:.8125rem;cursor:pointer;text-decoration:none}" +
      "#" + dialogId + " button:hover,#" + dialogId + " a:hover{background:var(--muted,#f3f4f6)}" +
      "#" + dialogId + " [data-upgrade]:not([hidden]){border-color:var(--primary,#2563eb);background:var(--primary,#2563eb);color:var(--primary-foreground,#fff);font-weight:600}" +
      "#" + dialogId + " [data-upgrade]:not([hidden]):hover{border-color:var(--primary,#2563eb);background:var(--primary,#2563eb);filter:brightness(.94)}" +
      "#" + dialogId + " button:disabled{cursor:not-allowed;opacity:.6}" +
      "#" + dialogId + " .epusdt-update-spinner{display:inline-block;width:.85rem;height:.85rem;margin-right:.35rem;border:2px solid currentColor;border-right-color:transparent;border-radius:50%;vertical-align:-.12rem;animation:epusdt-update-spin .7s linear infinite}" +
      "@media (max-width:480px){#" + dialogId + " .epusdt-update-head{padding:.875rem 4.75rem .875rem .875rem;gap:.5rem}#" + dialogId + " [data-close]{top:.45rem;right:.5rem}#" + dialogId + " .epusdt-update-body{max-height:calc(100vh - 8rem);max-height:calc(100dvh - 8rem);padding:.875rem;font-size:.8125rem}#" + dialogId + " dl{grid-template-columns:minmax(0,1fr);gap:.15rem .5rem}#" + dialogId + " dt{font-weight:600;margin-top:.35rem}#" + dialogId + " dd{margin-bottom:.25rem}#" + dialogId + " .epusdt-update-foot{padding:.65rem .875rem}#" + dialogId + " .epusdt-update-foot button,#" + dialogId + " .epusdt-update-foot a{flex:1 1 calc(50% - .25rem);text-align:center}}" +
      "@keyframes epusdt-update-spin{to{transform:rotate(360deg)}}";
    document.head.appendChild(style);
  }

  function createDialog() {
    var existing = document.getElementById(dialogId);
    if (existing) return existing;
    addStyles();
    var text = labels();
    var dialog = document.createElement("dialog");
    dialog.id = dialogId;
    dialog.innerHTML =
      '<div class="epusdt-update-head"><h2></h2><button type="button" data-close></button></div>' +
      '<div class="epusdt-update-body"><div class="epusdt-update-state" data-state role="status" aria-live="polite" aria-atomic="true"></div><dl data-details hidden></dl><div data-notes-wrap hidden><div data-notes-title></div><pre data-notes></pre></div></div>' +
      '<div class="epusdt-update-foot"><button type="button" data-retry></button><button type="button" data-upgrade hidden></button></div>';
    dialog.querySelector("h2").textContent = text.title;
    dialog.querySelector("[data-close]").textContent = text.close;
    dialog.querySelector("[data-retry]").textContent = text.retry;
    dialog.querySelector("[data-close]").addEventListener("click", function () {
      dialog.close();
    });
    dialog.querySelector("[data-retry]").addEventListener("click", function () {
      check(dialog);
    });
    dialog.querySelector("[data-upgrade]").textContent = text.upgrade;
    dialog.querySelector("[data-upgrade]").addEventListener("click", function () {
      startUpgrade(dialog);
    });
    dialog.addEventListener("close", function () {
      requestGeneration += 1;
      if (requestController) requestController.abort();
      requestController = null;
      if (timeoutId) window.clearTimeout(timeoutId);
      timeoutId = null;
      requestInFlight = false;
      stopUpdatePolling();
    });
    document.body.appendChild(dialog);
    return dialog;
  }

  function setState(dialog, className, message, loading) {
    var state = dialog.querySelector("[data-state]");
    state.className = "epusdt-update-state " + (className || "");
    state.textContent = "";
    if (loading) {
      var spinner = document.createElement("span");
      spinner.className = "epusdt-update-spinner";
      state.appendChild(spinner);
    }
    state.appendChild(document.createTextNode(message));
  }

  function safeReleaseURL(result) {
    try {
      var url = new URL(result.release_url || "");
      if (url.origin !== "https://github.com" || url.username || url.password || url.search || url.hash) return "";
      var parts = url.pathname.split("/").filter(Boolean);
      if (parts.length !== 5 || !parts[0] || !parts[1] || parts[2] !== "releases" || parts[3] !== "tag") return "";
      return decodeURIComponent(parts[4]) === result.latest_version ? url.href : "";
    } catch (_) {
      return "";
    }
  }

  function setDetails(dialog, result) {
    var text = labels();
    latestVersion = result.latest_version || latestVersion;
    var releaseURL = safeReleaseURL(result);
    latestHasUpdate = !!(result.has_update && updateCapability && updateCapability.can_update);
    if (versionContainer) {
      var updateLink = versionContainer.querySelector("[" + linkAttribute + "]");
      if (updateLink) updateLink.hidden = !latestHasUpdate;
    }
    var details = dialog.querySelector("[data-details]");
    details.hidden = false;
    details.textContent = "";
    [
      { label: text.current, value: result.current_version, badge: true },
      { label: text.latest, value: result.latest_version, href: releaseURL },
    ].forEach(function (row) {
      var dt = document.createElement("dt");
      dt.textContent = row.label;
      var dd = document.createElement("dd");
      if (row.badge && row.value) {
        var versionValue = document.createElement("span");
        versionValue.className = "epusdt-version-value";
        versionValue.textContent = row.value;
        dd.appendChild(versionValue);
      } else if (row.href && row.value) {
        var releaseLink = document.createElement("a");
        releaseLink.className = "epusdt-version-link";
        releaseLink.href = row.href;
        releaseLink.target = "_blank";
        releaseLink.rel = "noopener noreferrer";
        releaseLink.textContent = row.value;
        dd.appendChild(releaseLink);
      } else {
        dd.textContent = row.value || text.unknown;
      }
      details.appendChild(dt);
      details.appendChild(dd);
    });
    if (result.published_at) {
      var published = new Date(result.published_at);
      if (!isNaN(published.getTime())) {
        var dt = document.createElement("dt");
        dt.textContent = text.published;
        var dd = document.createElement("dd");
        dd.textContent = published.toLocaleString();
        details.appendChild(dt);
        details.appendChild(dd);
      }
    }
    var notesWrap = dialog.querySelector("[data-notes-wrap]");
    notesWrap.hidden = !result.release_notes;
    if (result.release_notes) {
      dialog.querySelector("[data-notes-title]").textContent = text.notes;
      // Release notes are deliberately shown as text. GitHub content is not
      // trusted and this avoids introducing a Markdown/HTML XSS sink.
      dialog.querySelector("[data-notes]").textContent = result.release_notes;
    }
    var upgrade = dialog.querySelector("[data-upgrade]");
    upgrade.hidden = !(result.has_update && updateCapability && updateCapability.can_update);
    upgrade.disabled = false;
    upgrade.textContent = text.upgrade;
  }

  function stopUpdatePolling() {
    if (updatePollTimer) window.clearTimeout(updatePollTimer);
    updatePollTimer = null;
  }

  function fetchWithTimeout(url, options, timeout) {
    var controller = new AbortController();
    var timer = window.setTimeout(function () { controller.abort(); }, timeout);
    return fetch(url, Object.assign({}, options, { signal: controller.signal })).finally(function () {
      window.clearTimeout(timer);
    });
  }

  async function loadCapability() {
    var token = tokenFromCookie();
    updateCapability = null;
    if (!token) return null;
    try {
      var response = await fetchWithTimeout("/admin/api/v1/system/update/capability", {
        headers: { Authorization: "Bearer " + token, Accept: "application/json" },
        cache: "no-store",
      }, 8000);
      if (!response.ok) return null;
      var payload = await response.json();
      updateCapability = payload.data || null;
      return updateCapability;
    } catch (error) {
      if (error && error.name === "AbortError") throw error;
      return null;
    }
  }

  async function pollUpdate(dialog) {
    if (!dialog.open) return;
    var token = tokenFromCookie();
    var text = labels();
    var retryDelay = 1000;
    function scheduleNext() {
      if (Date.now() >= updateDeadline) {
        stopUpdatePolling();
        setState(dialog, "error", text.updateTimeout, false);
        dialog.querySelector("[data-retry]").hidden = false;
        return;
      }
      updatePollTimer = window.setTimeout(function () { pollUpdate(dialog); }, retryDelay);
    }
    async function verifyTargetVersion() {
      if (!latestVersion) return false;
      var headers = { Authorization: "Bearer " + token, Accept: "application/json" };
      var confirmedVersion = "";
      try {
        var response = await fetchWithTimeout("/admin/api/v1/system/version/current?target_version=" + encodeURIComponent(latestVersion), {
          headers: headers,
          cache: "no-store",
        }, 8000);
        if (response.ok) {
          var payload = await response.json();
          var result = payload.data;
          if (result && (result.confirmed || result.current_version === latestVersion)) {
            confirmedVersion = result.current_version;
          }
        }
      } catch (_) {}
      if (!confirmedVersion) {
        try {
          var publicResponse = await fetchWithTimeout("/payments/gmpay/v1/config", {
            headers: { Accept: "application/json" },
            cache: "no-store",
          }, 5000);
          if (publicResponse.ok) {
            var publicPayload = await publicResponse.json();
            var publicVersion = publicPayload && publicPayload.data && publicPayload.data.version;
            if (publicVersion === latestVersion) confirmedVersion = publicVersion;
          }
        } catch (_) {}
      }
      if (!confirmedVersion) {
        try {
          var response = await fetchWithTimeout("/admin/api/v1/system/version/check", {
            headers: headers,
            cache: "no-store",
          }, 12000);
          if (response.ok) {
            var payload = await response.json();
            var result = payload.data;
            if (result && result.current_version === latestVersion) {
              confirmedVersion = result.current_version;
            }
          }
        } catch (_) {}
      }
      if (!confirmedVersion) return false;
      latestVersion = confirmedVersion;
      stopUpdatePolling();
      setState(dialog, "success", text.updated + " " + confirmedVersion, false);
      var retry = dialog.querySelector("[data-retry]");
      retry.hidden = true;
      var upgrade = dialog.querySelector("[data-upgrade]");
      upgrade.hidden = true;
      window.setTimeout(function () {
        if (dialog.open) window.location.reload();
      }, 1500);
      return true;
    }
    try {
      var response = await fetchWithTimeout("/admin/api/v1/system/update/status", {
        headers: { Authorization: "Bearer " + token, Accept: "application/json" },
        cache: "no-store",
      }, 8000);
      if (!response.ok) {
        var statusError = new Error("status unavailable");
        statusError.status = response.status;
        throw statusError;
      }
      var payload = await response.json();
      var state = payload.data || {};
      updatePollErrors = 0;
      if (state.status === "running") {
        updateWasRunning = true;
        var stage = text.stages[state.stage] || text.upgrading;
        var progress = state.stage === "downloading" && Number(state.percent) > 0 ? " " + state.percent + "%" : "";
        setState(dialog, "", stage + progress, true);
        scheduleNext();
        return;
      }
      if (state.status === "succeeded") {
        updateWasRunning = true;
        if (state.restart_required && updateCapability && !updateCapability.can_restart) {
          stopUpdatePolling();
          latestHasUpdate = false;
          if (versionContainer) {
            var updateLink = versionContainer.querySelector("[" + linkAttribute + "]");
            if (updateLink) updateLink.hidden = true;
          }
          setState(dialog, "success", text.updatedManual, false);
          return;
        }
        setState(dialog, "", text.verifyingVersion, true);
        if (await verifyTargetVersion()) return;
        scheduleNext();
        return;
      }
      if (state.status === "failed") {
        stopUpdatePolling();
        setState(dialog, "error", state.error || text.failed, false);
        dialog.querySelector("[data-retry]").hidden = false;
        return;
      }
      // The updater process exits after swapping the binary. Its replacement
      // starts with an idle in-memory state, so verify the requested version.
      if (updateWasRunning || latestVersion) {
        setState(dialog, "", text.verifyingVersion, true);
        if (await verifyTargetVersion()) return;
      }
      scheduleNext();
    } catch (error) {
      // The service can be briefly unreachable while systemd starts the new
      // process. Keep polling until the version is confirmed or the deadline.
      try {
        if (await verifyTargetVersion()) return;
      } catch (_) {}
      updatePollErrors += 1;
      if (error && (error.status === 401 || error.status === 403)) {
        stopUpdatePolling();
        setState(dialog, "error", text.failed, false);
        dialog.querySelector("[data-retry]").hidden = false;
        return;
      }
      if (updatePollErrors >= 2) setState(dialog, "restarting", text.reconnecting, true);
      scheduleNext();
    }
  }

  async function startUpgrade(dialog) {
    if (requestInFlight || !updateCapability?.can_update) return;
    var text = labels();
    var token = tokenFromCookie();
    var button = dialog.querySelector("[data-upgrade]");
    updateWasRunning = false;
    updatePollErrors = 0;
    requestInFlight = true;
    button.disabled = true;
    setState(dialog, "", text.upgrading, true);
    try {
      var response = await fetchWithTimeout("/admin/api/v1/system/update/start", {
        method: "POST",
        headers: { Authorization: "Bearer " + token, Accept: "application/json" },
        cache: "no-store",
      }, 20000);
      var payload = await response.json().catch(function () { return {}; });
      if (!response.ok) throw new Error(response.status === 422 ? text.upgradeUnsupported : text.failed);
      var state = payload.data || {};
      updateDeadline = Date.now() + Math.max(1, Number(state.timeout_seconds) || 600) * 1000 + 120000;
      latestVersion = state.target_version || latestVersion;
      updateWasRunning = state.status === "running";
      button.hidden = true;
      pollUpdate(dialog);
    } catch (error) {
      setState(dialog, "error", error && error.name === "AbortError" ? text.timeout : (error.message || text.failed), false);
      button.disabled = false;
      dialog.querySelector("[data-retry]").hidden = false;
    } finally {
      requestInFlight = false;
    }
  }

  async function check(dialog) {
    if (requestInFlight) return;
    var text = labels();
    var token = tokenFromCookie();
    if (!token) return;
    requestInFlight = true;
    var generation = ++requestGeneration;
    var retry = dialog.querySelector("[data-retry]");
    retry.hidden = true;
    dialog.querySelector("[data-details]").hidden = true;
    dialog.querySelector("[data-notes-wrap]").hidden = true;
    setState(dialog, "", text.checking, true);
    requestController = new AbortController();
    timeoutId = window.setTimeout(function () {
      if (requestController) requestController.abort();
    }, 12000);
    try {
      var response = await fetch("/admin/api/v1/system/version/check", {
        headers: { Authorization: "Bearer " + token, Accept: "application/json" },
        cache: "no-store",
        signal: requestController.signal,
      });
      var payload = await response.json().catch(function () { return {}; });
      if (generation !== requestGeneration) return;
      if (!response.ok) {
        var message = response.status === 404 ? text.noRelease : response.status === 429 ? text.rate : response.status === 504 ? text.timeout : text.failed;
        throw new Error(message);
      }
      var result = payload.data;
      if (!result || !result.current_version || !result.latest_version) throw new Error(text.failed);
      if (!await loadCapability()) throw new Error(text.failed);
      var canUpdate = result.has_update && updateCapability && updateCapability.can_update;
      setState(dialog, canUpdate ? "warning" : "success", result.has_update && !canUpdate ? text.upgradeUnsupported : (result.has_update ? text.updateState : text.latestState), false);
      setDetails(dialog, result);
    } catch (error) {
      if (generation !== requestGeneration) return;
      var message = error && error.name === "AbortError" ? text.timeout : (error && error.message) || text.failed;
      setState(dialog, "error", message, false);
      retry.hidden = false;
    } finally {
      if (generation !== requestGeneration) return;
      if (timeoutId) window.clearTimeout(timeoutId);
      timeoutId = null;
      requestController = null;
      requestInFlight = false;
    }
  }

  function installUpdateLink(container) {
    if (!tokenFromCookie()) return;
    var text = labels();
    var links = Array.from(container.querySelectorAll("a"));
    var link = links.find(function (candidate) {
      return /^(更新|update)$/i.test(candidate.textContent.trim());
    }) || links.find(function (candidate) {
      return candidate.getAttribute(linkAttribute) === "bound";
    });
    if (!link) {
      link = document.createElement("a");
      link.className = "shrink-0 font-medium text-primary leading-none hover:text-primary/80";
      link.textContent = text.update;
      container.appendChild(link);
    }
    var alreadyBound = link.getAttribute(linkAttribute) === "bound";
    link.hidden = latestHasUpdate !== true;
    // React and the observer can run in either order. Remove duplicate update
    // anchors so the version area always contains one action link.
    links.forEach(function (candidate) {
      if (candidate !== link && /^(更新|update)$/i.test(candidate.textContent.trim())) {
        candidate.remove();
      }
    });
    link.href = "#";
    link.target = "_self";
    link.rel = "";
    link.title = text.button;
    link.setAttribute("aria-label", text.button);
    if (!alreadyBound) link.setAttribute(linkAttribute, "bound");
    versionContainer = container;
  }

  function handleUpdateEntry(event) {
    var target = event.target;
    var link = target && target.closest ? target.closest("a") : null;
    if (!link || !/^(更新|update)$/i.test(link.textContent.trim())) return;
    var container = link.closest(".ml-auto.flex.items-center.gap-0.whitespace-nowrap");
    if (!container) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    versionContainer = container;
    var dialog = createDialog();
    if (!dialog.open) dialog.showModal();
    check(dialog);
  }

  function scan() {
    if (!tokenFromCookie()) return;
    document.querySelectorAll(".ml-auto.flex.items-center.gap-0.whitespace-nowrap").forEach(installUpdateLink);
    scheduleBackgroundCheck(250);
  }

  function scheduleBackgroundCheck(delay) {
    if (backgroundCheckTimer || backgroundCheckInFlight || !tokenFromCookie()) return;
    backgroundCheckTimer = window.setTimeout(function () {
      backgroundCheckTimer = null;
      var token = tokenFromCookie();
      if (!token) return;
      backgroundCheckInFlight = true;
      fetchWithTimeout("/admin/api/v1/system/version/check", {
        headers: { Authorization: "Bearer " + token, Accept: "application/json" },
        cache: "no-store",
      }, 12000).then(function (response) {
        if (!response.ok) return null;
        return response.json();
      }).then(async function (payload) {
        var result = payload && payload.data;
        if (!result) {
          latestHasUpdate = null;
          if (versionContainer) {
            var retryLink = versionContainer.querySelector("[" + linkAttribute + "]");
            if (retryLink) retryLink.hidden = false;
          }
          return;
        }
        await loadCapability();
        latestHasUpdate = !!(result && result.has_update && (!updateCapability || updateCapability.can_update));
        if (versionContainer) {
          var link = versionContainer.querySelector("[" + linkAttribute + "]");
          if (link) link.hidden = !latestHasUpdate;
        }
      }).catch(function () {
        latestHasUpdate = null;
        if (versionContainer) {
          var retryLink = versionContainer.querySelector("[" + linkAttribute + "]");
          if (retryLink) retryLink.hidden = false;
        }
      }).finally(function () {
        backgroundCheckInFlight = false;
        if (tokenFromCookie()) {
          backgroundCheckTimer = window.setTimeout(function () {
            backgroundCheckTimer = null;
            scheduleBackgroundCheck(0);
          }, 60000);
        }
      });
    }, delay === undefined ? 0 : delay);
  }

  if (typeof MutationObserver !== "undefined") {
    new MutationObserver(scan).observe(document.documentElement, { childList: true, subtree: true });
  }
  document.addEventListener("click", handleUpdateEntry, true);
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", scan);
  else scan();
  scheduleBackgroundCheck();
})();
