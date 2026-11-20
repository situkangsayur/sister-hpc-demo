"use client";

import { useState } from "react";
import Storefront from "./components/Storefront";
import Timeline from "./components/Timeline";
import { BreakerPanel, FaultPanel, HealthBar, LabPanel, NotificationPanel, OpenSearchPanel, SagaPanel } from "./components/Panels";

export default function Page() {
  const [filter, setFilter] = useState("");
  return (
    <main>
      <header>
        <h1>Toko Daring Microservices</h1>
        <div className="sub">
          Bab 5 · slide P05 · APISIX → nginx → order ×2 · catalog + OpenSearch · inventory · payment · notification · NATS JetStream · Postgres
        </div>
      </header>
      <HealthBar />
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
    </main>
  );
}
