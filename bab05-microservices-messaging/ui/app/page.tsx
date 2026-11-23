"use client";

import { useEffect, useState } from "react";
import BankTab from "./components/Bank";
import Storefront from "./components/Storefront";
import Timeline from "./components/Timeline";
import { BreakerPanel, FaultPanel, HealthBar, LabPanel, NotificationPanel, OpenSearchPanel, SagaPanel } from "./components/Panels";

export default function Page() {
  const [filter, setFilter] = useState("");
  const [tab, setTab] = useState<"toko" | "bank">("toko");
  // ?tab=bank membuka tab Bank langsung (untuk proyektor atau tautan dari materi DE).
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("tab") === "bank") setTab("bank");
  }, []);
  return (
    <main>
      <header>
        <h1>Toko Daring Microservices + Bank Syariah</h1>
        <div className="sub">
          Bab 5 · slide P05 · APISIX → nginx → order ×2 · catalog + OpenSearch · inventory · payment · bank · notification · NATS JetStream (SHOP, BANK) · Postgres
        </div>
      </header>
      <HealthBar />
      <nav className="tabs">
        <button className={tab === "toko" ? "" : "ghost"} onClick={() => setTab("toko")}>
          Toko &amp; saga
        </button>
        <button className={tab === "bank" ? "" : "ghost"} onClick={() => setTab("bank")}>
          Bank
        </button>
      </nav>
      {tab === "toko" ? (
        <div className="grid">
          <Storefront onOrder={setFilter} />
          <div className="col">
            <FaultPanel />
            <BreakerPanel />
          </div>
          <SagaPanel onSelect={setFilter} selected={filter} />
          <Timeline filter={filter} setFilter={setFilter} />
          <LabPanel />
          <OpenSearchPanel />
          <NotificationPanel />
        </div>
      ) : (
        <div className="grid">
          <BankTab />
          <Timeline filter={filter} setFilter={setFilter} />
        </div>
      )}
    </main>
  );
}
