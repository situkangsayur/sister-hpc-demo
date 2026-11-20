"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";

const menu = [
  ["/", "Protocol Playground"],
  ["/realtime", "Real-time"],
  ["/andal", "Timeout & Retry"],
  ["/gateway", "Gateway & GraphQL N+1"],
];

export default function Nav() {
  const p = usePathname();
  return (
    <nav>
      {menu.map(([href, label]) => (
        <Link key={href} href={href} className={p === href ? "aktif" : ""}>
          {label}
        </Link>
      ))}
    </nav>
  );
}
