(() => {
  const WINGNEWS_BASE_URL = "https://news.wingman.actor";
  const BUTTON_ID = "wingnews-open-button";
  const STYLE_ID = "wingnews-open-button-style";
  const LOGO_URL = chrome.runtime.getURL("WingmanBlue.png");

  function getItemIdFromURL(rawURL) {
    if (!rawURL) {
      return null;
    }

    try {
      const url = new URL(rawURL);
      if (url.hostname !== "news.ycombinator.com" || url.pathname !== "/item") {
        return null;
      }

      const id = url.searchParams.get("id");
      if (!id || !/^\d+$/.test(id)) {
        return null;
      }

      return id;
    } catch {
      return null;
    }
  }

  function buildWingNewsURL(itemId) {
    return `${WINGNEWS_BASE_URL}/item/${itemId}`;
  }

  function injectStyle() {
    if (document.getElementById(STYLE_ID)) {
      return;
    }

    const style = document.createElement("style");
    style.id = STYLE_ID;
    style.textContent = `
      #${BUTTON_ID} {
        position: fixed;
        right: 16px;
        bottom: 16px;
        z-index: 2147483647;
        display: inline-flex;
        align-items: center;
        gap: 8px;
        padding: 10px 12px;
        border: 1px solid #2a2a2a;
        border-radius: 6px;
        background: linear-gradient(140deg, #181818 0%, #121212 100%);
        color: #e8e8e8;
        text-decoration: none;
        font: 600 12px/1 "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
        letter-spacing: 0.02em;
        box-shadow: 0 12px 24px rgba(0, 0, 0, 0.28);
        transition: transform 120ms ease, box-shadow 120ms ease, border-color 120ms ease;
      }

      #${BUTTON_ID}:hover {
        transform: translateY(-1px);
        border-color: #3d88c5;
        box-shadow: 0 16px 30px rgba(0, 0, 0, 0.32);
      }

      #${BUTTON_ID}:focus-visible {
        outline: 2px solid #3d88c5;
        outline-offset: 2px;
      }

      #${BUTTON_ID} .wingnews-logo {
        width: 22px;
        height: 16px;
        display: block;
        object-fit: contain;
        filter: drop-shadow(0 0 6px rgba(61, 136, 197, 0.45));
      }

      @media (max-width: 640px) {
        #${BUTTON_ID} {
          right: 10px;
          left: 10px;
          bottom: 10px;
          justify-content: center;
          padding: 11px 12px;
          font-size: 13px;
        }
      }
    `;

    document.documentElement.appendChild(style);
  }

  function injectButton(itemId) {
    if (document.getElementById(BUTTON_ID)) {
      return;
    }

    const button = document.createElement("a");
    button.id = BUTTON_ID;
    button.href = buildWingNewsURL(itemId);
    button.setAttribute("aria-label", "Open this post in WingNews");
    button.innerHTML = `<img class="wingnews-logo" src="${LOGO_URL}" alt="" aria-hidden="true"/><span>Open in WingNews</span>`;

    document.body.appendChild(button);
  }

  const itemId = getItemIdFromURL(window.location.href);
  if (!itemId || !document.body) {
    return;
  }

  injectStyle();
  injectButton(itemId);
})();
