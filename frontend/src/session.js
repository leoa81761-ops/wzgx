import { reactive } from "vue";
import { api, clearToken, getToken, setToken } from "./api";

export const session = reactive({
  user: null,
  ready: false,
});

export async function loadSession() {
  if (!getToken()) {
    session.user = null;
    session.ready = true;
    return null;
  }
  try {
    session.user = await api("/api/auth/me");
    const status = await api("/api/me/consent-status");
    session.user.needs_reconsent = !!status.needs_reconsent;
    session.user.legal_version = status.legal_version || "";
  } catch {
    clearToken();
    session.user = null;
  }
  session.ready = true;
  return session.user;
}

export async function acceptLogin(data) {
  setToken(data.token);
  session.user = data.user;
  session.ready = true;
  try {
    const status = await api("/api/me/consent-status");
    session.user.needs_reconsent = !!status.needs_reconsent;
    session.user.legal_version = status.legal_version || "";
  } catch {
    session.user.needs_reconsent = false;
  }
}

export function logout() {
  clearToken();
  session.user = null;
}
