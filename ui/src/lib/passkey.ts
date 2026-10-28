import { ApiError, api } from "./api";

/**
 * WebAuthn 的浏览器侧胶水（照 herdr-web 的 web/src/lib/passkey.ts）。只在远程那个 origin 上能用：
 * passkey 绑在中继上那个主机名上，本机的 http://127.0.0.1 不是它（服务端那几个口直接 409）。
 * 没配对 / 要重验时的登录在服务端自带的配对页里（internal/server/pairpage.html），这里只管已经进来之后的事。
 *
 * 服务端（go-webauthn）用 base64url 传 challenge / credential id 这些字节串，浏览器 API 要 ArrayBuffer，
 * 返回的又是 ArrayBuffer —— 两个方向都得转。`PublicKeyCredential.toJSON()` 能省掉这段，但 Safari 18 才有。
 */

function fromB64u(s: string): Uint8Array<ArrayBuffer> {
  const b = atob(s.replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(b, (c) => c.charCodeAt(0));
}

function toB64u(b: ArrayBuffer): string {
  let s = "";
  for (const byte of new Uint8Array(b)) s += String.fromCharCode(byte);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export const passkeySupported = () => typeof PublicKeyCredential !== "undefined" && !!navigator.credentials;

/**
 * 「人划掉的」和「浏览器不让做的」在错误对象上一模一样（都是 NotAllowedError，规范故意的 —— 不让网页
 * 试探出这台设备上有没有某把 passkey）。唯一分得开的信号是**时间**：真弹出系统面板再让人划掉，最少大半秒；
 * 被策略挡掉是当场就回。快回来的不当取消，照旧报出来 —— 吞成静默的话就是「点了没反应、而且查不出原因」。
 */
const CANCEL_MS = 700;

class Blocked extends Error {}

export function isCancel(e: unknown) {
  if (e instanceof Blocked) return false;
  const n = (e as { name?: string } | null)?.name;
  return n === "AbortError" || n === "NotAllowedError";
}

async function ask<T>(run: () => Promise<T>): Promise<T> {
  const t0 = performance.now();
  try {
    return await run();
  } catch (e) {
    if ((e as { name?: string }).name === "NotAllowedError" && performance.now() - t0 < CANCEL_MS) {
      throw new Blocked("浏览器没让做这次验证，而且不说原因。最常见的是这台设备上没有这个站点的 passkey，或者地址栏显示「不安全」。");
    }
    throw e;
  }
}

type Begin<T> = { options: { publicKey: T }; ceremony: string };
type CreateJSON = { challenge: string; user: { id: string; name: string; displayName: string }; excludeCredentials?: { id: string; type: string }[] };
type RequestJSON = { challenge: string; allowCredentials?: { id: string; type: string }[] };

/**
 * 注册一把新 passkey。**已经有 passkey 时服务端会先要一次 step-up**（403 reason=stepup，见
 * internal/auth 的 StepUpNeeded）：拿配对码进来的人不能给自己偷偷再留一把。step-up 有两种做法，让人选
 * （RemoteSettings）：用已有的 passkey 验一次（`stepUpWithPasskey`），或者到电脑上批准（auth/approve.go）——
 * 新手机上一把 passkey 都没有，只给前一种的话系统弹「未发现任何通行密钥」，人就卡死了。
 */
export const registerPasskey = () => doRegister();

export const isStepUp = (e: unknown) => e instanceof ApiError && e.reason === "stepup";

/** 用已有的 passkey 做 step-up，再注册。 */
export async function stepUpWithPasskey(): Promise<string> {
  await loginPasskey();
  return await doRegister();
}

async function doRegister(): Promise<string> {
  const { options, ceremony } = await api.passkeyStep<Begin<CreateJSON>>("register", "begin");
  const pk = options.publicKey;
  const req = {
    ...pk,
    challenge: fromB64u(pk.challenge),
    user: { ...pk.user, id: fromB64u(pk.user.id) },
    excludeCredentials: (pk.excludeCredentials ?? []).map((c) => ({ ...c, id: fromB64u(c.id) })),
  } as unknown as PublicKeyCredentialCreationOptions;
  const cred = (await ask(() => navigator.credentials.create({ publicKey: req }))) as PublicKeyCredential | null;
  if (!cred) throw new Error("浏览器没有返回凭据");
  const att = cred.response as AuthenticatorAttestationResponse;
  const r = await api.passkeyStep<{ label: string }>(
    "register",
    "finish",
    {
      id: cred.id,
      rawId: toB64u(cred.rawId),
      type: cred.type,
      clientExtensionResults: cred.getClientExtensionResults(),
      response: { clientDataJSON: toB64u(att.clientDataJSON), attestationObject: toB64u(att.attestationObject) },
    },
    ceremony,
  );
  return r.label;
}

/** 用 passkey 验一次（discoverable 流程：不用先说自己是谁，手机上就是「点一下 → Face ID」）。 */
export async function loginPasskey(): Promise<string> {
  const { options, ceremony } = await api.passkeyStep<Begin<RequestJSON>>("login", "begin");
  const pk = options.publicKey;
  const req = {
    ...pk,
    challenge: fromB64u(pk.challenge),
    allowCredentials: (pk.allowCredentials ?? []).map((c) => ({ ...c, id: fromB64u(c.id) })),
  } as unknown as PublicKeyCredentialRequestOptions;
  const cred = (await ask(() => navigator.credentials.get({ publicKey: req }))) as PublicKeyCredential | null;
  if (!cred) throw new Error("浏览器没有返回凭据");
  const asr = cred.response as AuthenticatorAssertionResponse;
  const r = await api.passkeyStep<{ label: string }>(
    "login",
    "finish",
    {
      id: cred.id,
      rawId: toB64u(cred.rawId),
      type: cred.type,
      clientExtensionResults: cred.getClientExtensionResults(),
      response: {
        clientDataJSON: toB64u(asr.clientDataJSON),
        authenticatorData: toB64u(asr.authenticatorData),
        signature: toB64u(asr.signature),
        userHandle: asr.userHandle ? toB64u(asr.userHandle) : undefined,
      },
    },
    ceremony,
  );
  return r.label;
}
