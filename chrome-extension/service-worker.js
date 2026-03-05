const WINGNEWS_BASE_URL = "https://news.wingman.actor";

function getItemIdFromURL(rawURL) {
  if (!rawURL) {
    return null;
  }

  try {
    const url = new URL(rawURL);
    if (url.hostname !== "news.ycombinator.com") {
      return null;
    }

    if (url.pathname !== "/item") {
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

function openInWingNews(tab) {
  const itemId = getItemIdFromURL(tab && tab.url);
  if (!itemId) {
    return;
  }

  if (tab && typeof tab.id === "number") {
    chrome.tabs.update(tab.id, { url: buildWingNewsURL(itemId) });
    return;
  }

  chrome.tabs.create({ url: buildWingNewsURL(itemId) });
}

function updateActionState(tabId, url) {
  const itemId = getItemIdFromURL(url);
  if (itemId) {
    chrome.action.enable(tabId);
    chrome.action.setTitle({ tabId, title: "Open this post in WingNews" });
    return;
  }

  chrome.action.disable(tabId);
  chrome.action.setTitle({ tabId, title: "Open in WingNews (HN item page only)" });
}

function refreshActionStateForAllTabs() {
  chrome.tabs.query({}, (tabs) => {
    if (chrome.runtime.lastError || !Array.isArray(tabs)) {
      return;
    }

    for (const tab of tabs) {
      if (typeof tab.id !== "number") {
        continue;
      }

      updateActionState(tab.id, tab.url);
    }
  });
}

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: "open-in-wingnews",
      title: "Open in WingNews",
      contexts: ["page"],
      documentUrlPatterns: ["https://news.ycombinator.com/item?id=*"]
    });
  });

  refreshActionStateForAllTabs();
});

chrome.runtime.onStartup.addListener(() => {
  refreshActionStateForAllTabs();
});

chrome.action.onClicked.addListener((tab) => {
  openInWingNews(tab);
});

chrome.contextMenus.onClicked.addListener((info, tab) => {
  if (info.menuItemId !== "open-in-wingnews") {
    return;
  }

  openInWingNews(tab);
});

chrome.tabs.onActivated.addListener(({ tabId }) => {
  chrome.tabs.get(tabId, (tab) => {
    if (!tab || chrome.runtime.lastError) {
      return;
    }

    updateActionState(tabId, tab.url);
  });
});

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (typeof changeInfo.url === "string") {
    updateActionState(tabId, changeInfo.url);
    return;
  }

  if (changeInfo.status === "complete") {
    updateActionState(tabId, tab.url);
  }
});
