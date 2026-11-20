// Konfigurasi runtime untuk browser (port host yang dibuka compose).
export const dynamic = "force-dynamic";

export function GET() {
  return Response.json({
    chatWsPort: process.env.CHAT_WS_PORT ?? "50202",
    chatTcpPort: process.env.CHAT_TCP_PORT ?? "50210",
    echoTcpPort: process.env.ECHO_TCP_PORT ?? "50220",
    echoUdpPort: process.env.ECHO_UDP_PORT ?? "50221",
  });
}
