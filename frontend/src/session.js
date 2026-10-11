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
}

export function logout() {
  clearToken();
  session.user = null;
}
