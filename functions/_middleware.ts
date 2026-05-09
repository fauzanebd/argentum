// SPA fallback: when downstream returns 404 for an HTML navigation,
// serve /index.html so client-side routing can take over. Non-HTML
// requests (assets, API JSON) keep their original 404.

export const onRequest: PagesFunction = async (context) => {
  const response = await context.next();

  if (response.status !== 404) return response;
  if (context.request.method !== "GET") return response;

  const accept = context.request.headers.get("accept") ?? "";
  if (!accept.includes("text/html")) return response;

  const url = new URL(context.request.url);
  if (url.pathname === "/index.html") return response;

  const indexResp = await fetch(new URL("/index.html", url.origin).toString());
  return new Response(indexResp.body, {
    status: 200,
    headers: indexResp.headers,
  });
};
