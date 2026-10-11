/** Outbound contact links. Same sales line as smartsoft.co.id. */

const WHATSAPP_NUMBER = "6281224212953";

export function whatsapp(message?: string) {
  const base = `https://wa.me/${WHATSAPP_NUMBER}`;
  return message ? `${base}?text=${encodeURIComponent(message)}` : base;
}

export const DEMO_LINK = whatsapp(
  "Hello SmartSoft — I would like a demo of Argentum.",
);

export const DEVELOPER_LINK = whatsapp(
  "Hello SmartSoft — I would like an Argentum API key to try the /v1 API.",
);

export const DOCS_LINK = "/docs/";
export const SMARTSOFT_LINK = "https://smartsoft.co.id";
