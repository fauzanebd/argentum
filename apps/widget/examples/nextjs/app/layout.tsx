export const metadata = { title: "Argentum widget — Next.js" };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body style={{ font: "15px/1.6 system-ui, sans-serif", margin: "3rem auto", maxWidth: "40rem", padding: "0 1rem" }}>
        {children}
      </body>
    </html>
  );
}
