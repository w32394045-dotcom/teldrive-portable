// API-key sign-in for the public deployment.
//
// The built-in login exchanges a Telegram account for a session cookie, which
// only makes sense for the machine that owns the account. When the same UI is
// published on the internet, the credential has to be an API key instead: it is
// 256 bits of server-generated randomness, it can be named, given an expiry and
// revoked on its own, and one leaked key never exposes the others.
//
// Two storage tiers on purpose:
//   sessionStorage  default. Survives reloads in this tab, disappears with it,
//                   and is not readable by another tab or a later session.
//   localStorage    only when the user explicitly asks to remember the device.
//                   More convenient, and correspondingly more exposed if the
//                   origin is ever compromised, so it is opt-in rather than the
//                   default.

const SESSION_KEY = "teldrive.apiKey";
const PERSISTED_KEY = "teldrive.apiKey.persisted";

function read(storage: Storage, name: string): string | null {
  try {
    const value = storage.getItem(name);
    return value?.trim() ? value.trim() : null;
  } catch {
    // Private-mode browsers can throw on storage access; an unreadable key just
    // means "not signed in", never a crash.
    return null;
  }
}

function write(storage: Storage, name: string, value: string) {
  try {
    storage.setItem(name, value);
  } catch {
    // Ignore: the in-memory value still works for this page view.
  }
}

function remove(storage: Storage, name: string) {
  try {
    storage.removeItem(name);
  } catch {
    // Ignore.
  }
}

let memoryKey: string | null = null;

/** The API key to send with every request, or null when signed out. */
export function readApiKey(): string | null {
  if (memoryKey) return memoryKey;
  return (
    read(window.localStorage, PERSISTED_KEY) ?? read(window.sessionStorage, SESSION_KEY)
  );
}

/** True when the key will survive closing the browser. */
export function isApiKeyPersisted(): boolean {
  return read(window.localStorage, PERSISTED_KEY) !== null;
}

export function writeApiKey(key: string, persist: boolean) {
  const value = key.trim();
  memoryKey = value;
  if (persist) {
    write(window.localStorage, PERSISTED_KEY, value);
    remove(window.sessionStorage, SESSION_KEY);
    return;
  }
  write(window.sessionStorage, SESSION_KEY, value);
  remove(window.localStorage, PERSISTED_KEY);
}

export function clearApiKey() {
  memoryKey = null;
  remove(window.sessionStorage, SESSION_KEY);
  remove(window.localStorage, PERSISTED_KEY);
}

/**
 * Whether this page is served from the machine that owns the drive.
 *
 * On loopback the owner gets the full Telegram sign-in. Anywhere else the
 * UI is a public surface and offers key sign-in only -- the account login is
 * pointless there and, with an empty `security.allowed-users`, actively unsafe.
 */
export function isLocalOrigin(): boolean {
  const host = window.location.hostname;
  return host === "localhost" || host === "127.0.0.1" || host === "[::1]" || host === "::1";
}

/** Whether the Telegram account sign-in should be offered on this origin. */
export function telegramSignInAllowed(): boolean {
  return isLocalOrigin();
}
