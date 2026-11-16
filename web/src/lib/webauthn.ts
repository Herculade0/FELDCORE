// Browser helpers for Citizen ID passkeys. The server speaks the WebAuthn
// JSON the go-webauthn library emits; the browser wants ArrayBuffers.

import { api } from "./api";

interface WireCred {
  id: string;
  type: PublicKeyCredentialType;
  transports?: AuthenticatorTransport[];
}

interface WireOptions {
  publicKey: {
    challenge: string;
    rpId?: string;
    timeout?: number;
    userVerification?: UserVerificationRequirement;
    attestation?: AttestationConveyancePreference;
    authenticatorSelection?: AuthenticatorSelectionCriteria;
    pubKeyCredParams?: PublicKeyCredentialParameters[];
    rp?: PublicKeyCredentialRpEntity;
    user?: { id: string; name: string; displayName: string };
    allowCredentials?: WireCred[];
    excludeCredentials?: WireCred[];
    extensions?: AuthenticationExtensionsClientInputs;
  };
}

export function passkeysAvailable(): boolean {
  return typeof window !== "undefined" && "PublicKeyCredential" in window;
}

function b64urlToBuf(value: string): ArrayBuffer {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (value.length % 4)) % 4);
  const bin = atob(padded);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

function bufToB64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function creds(list: WireCred[] | undefined): PublicKeyCredentialDescriptor[] | undefined {
  return list?.map((c) => ({ id: b64urlToBuf(c.id), type: c.type, transports: c.transports }));
}

function friendly(err: unknown): Error {
  if (err instanceof DOMException && err.name === "NotAllowedError") {
    return new Error("The passkey prompt was closed.");
  }
  return err instanceof Error ? err : new Error("That passkey could not be verified.");
}

function assertionJSON(cred: PublicKeyCredential) {
  const res = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment,
    response: {
      clientDataJSON: bufToB64url(res.clientDataJSON),
      authenticatorData: bufToB64url(res.authenticatorData),
      signature: bufToB64url(res.signature),
      userHandle: res.userHandle ? bufToB64url(res.userHandle) : null,
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}

function attestationJSON(cred: PublicKeyCredential) {
  const res = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment,
    response: {
      clientDataJSON: bufToB64url(res.clientDataJSON),
      attestationObject: bufToB64url(res.attestationObject),
      transports: typeof res.getTransports === "function" ? res.getTransports() : [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}

/** Discoverable passkey sign-in. The caller refreshes the session afterwards. */
export async function loginWithPasskey(): Promise<{ username: string; admin: boolean }> {
  const wire = await api<WireOptions>("/citizen/webauthn/login/begin", { method: "POST", quiet401: true });
  const pk = wire.publicKey;
  let cred: Credential | null;
  try {
    cred = await navigator.credentials.get({
      publicKey: {
        challenge: b64urlToBuf(pk.challenge),
        rpId: pk.rpId,
        timeout: pk.timeout,
        userVerification: pk.userVerification,
        allowCredentials: creds(pk.allowCredentials),
        extensions: pk.extensions,
      },
    });
  } catch (err) {
    throw friendly(err);
  }
  if (!(cred instanceof PublicKeyCredential)) throw new Error("This browser did not return a passkey.");
  return api("/citizen/webauthn/login/finish", { method: "POST", body: assertionJSON(cred), quiet401: true });
}

/** Register a passkey for the signed-in person. */
export async function registerPasskey(): Promise<void> {
  const wire = await api<WireOptions>("/citizen/webauthn/register/begin", { method: "POST" });
  const pk = wire.publicKey;
  if (!pk.user || !pk.rp || !pk.pubKeyCredParams) {
    throw new Error("Passkeys are not available on this address.");
  }
  let cred: Credential | null;
  try {
    cred = await navigator.credentials.create({
      publicKey: {
        challenge: b64urlToBuf(pk.challenge),
        rp: pk.rp,
        user: { id: b64urlToBuf(pk.user.id), name: pk.user.name, displayName: pk.user.displayName },
        pubKeyCredParams: pk.pubKeyCredParams,
        timeout: pk.timeout,
        excludeCredentials: creds(pk.excludeCredentials),
        authenticatorSelection: pk.authenticatorSelection,
        attestation: pk.attestation,
        extensions: pk.extensions,
      },
    });
  } catch (err) {
    throw friendly(err);
  }
  if (!(cred instanceof PublicKeyCredential)) throw new Error("This browser did not return a passkey.");
  await api("/citizen/webauthn/register/finish", { method: "POST", body: attestationJSON(cred) });
}
