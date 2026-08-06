import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { LocaleProvider } from "./i18n/LocaleProvider";
import i18n from "./i18n/config";
import App from "./App";
import "./styles.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <I18nextProvider i18n={i18n}>
      <LocaleProvider>
        <BrowserRouter basename="/dashboard">
          <App />
        </BrowserRouter>
      </LocaleProvider>
    </I18nextProvider>
  </React.StrictMode>,
);

if ("serviceWorker" in navigator && import.meta.env.PROD) {
  window.addEventListener("load", () => {
    void navigator.serviceWorker.register("/dashboard/sw.js").catch(() => undefined);
  });
}
