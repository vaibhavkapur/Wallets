import React from "react";
import { createRoot } from "react-dom/client";
import { OptimizerClient } from "@unowallet/optimizer-sdk";
import { App } from "./App";
import "./styles.css";

const client = new OptimizerClient({ baseUrl: "/api" });
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App client={client} />
  </React.StrictMode>,
);
