// Konfigurasi runtime untuk browser: port publik gateway dan grpcui.
export const dynamic = "force-dynamic";

export function GET() {
  return Response.json({
    gatewayPort: process.env.GATEWAY_PORT ?? "50401",
    grpcuiPort: process.env.GRPCUI_PORT ?? "50403",
  });
}
