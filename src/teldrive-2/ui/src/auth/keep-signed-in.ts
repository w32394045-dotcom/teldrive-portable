// Whether the sign-in should outlive the browser.
//
// Both sign-in paths need the same answer. The API key is stored by this module's
// caller in one of two browser stores; the session cookies are issued by the
// server, which cannot see a checkbox, so the choice travels as a request header
// that every call already carries.
//
// The default is to stay signed in. This is a drive that runs on the machine the
// user is sitting at, and being asked to fetch a phone and scan a QR code every
// time the browser closes is the behaviour that made "keep me signed in" worth
// asking for. The switch is on the login page, so opting out takes one click.

const KEEP_SIGNED_IN_KEY = "teldrive.keepSignedIn";

/** The header the server reads. Keep in step with `sessionPersistenceHeader`. */
export const PERSIST_HEADER = "X-Teldrive-Persist";

function readStored(): boolean | null {
  try {
    const value = window.localStorage.getItem(KEEP_SIGNED_IN_KEY);
    if (value === "true") return true;
    if (value === "false") return false;
    return null;
  } catch {
    // A browser that refuses storage is not a reason to guess wrong about a
    // preference; fall through to the default.
    return null;
  }
}

/** Whether the user asked to stay signed in. */
export function keepSignedIn(): boolean {
  return readStored() ?? true;
}

export function setKeepSignedIn(value: boolean) {
  try {
    window.localStorage.setItem(KEEP_SIGNED_IN_KEY, value ? "true" : "false");
  } catch {
    // Ignore: the choice still applies to this page view through the header.
  }
}

/** The header value the server expects for the current choice. */
export function persistHeaderValue(): string {
  return keepSignedIn() ? "persist" : "session";
}
