import { useState } from "react";
import { useTranslation } from "react-i18next";

import * as api from "@/api/extensions/netbird.ts";

import { ExtensionInstallResult } from "../vpn/extension-install-result.tsx";
import { ErrorHelp } from "./error-help.tsx";

type InstallProps = {
  setIsLocked: (isLocked: boolean) => void;
  onSuccess: () => void;
};

export const Install = ({ setIsLocked, onSuccess }: InstallProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [errMsg, setErrMsg] = useState("");

  function install() {
    if (isLoading) return;
    setIsLocked(true);
    setIsLoading(true);

    api
      .install()
      .then((rsp) => {
        if (rsp.code !== 0) {
          setErrMsg(rsp.msg);
          return;
        }

        onSuccess();
      })
      .catch((err) => {
        setErrMsg(err.message || "Install failed");
      })
      .finally(() => {
        setIsLoading(false);
        setIsLocked(false);
      });
  }

  return (
    <>
      <ExtensionInstallResult
        title={t("settings.netbird.notInstall")}
        description={t("settings.netbird.installDescription")}
        actionLabel={isLoading ? t("settings.netbird.installing") : t("settings.netbird.install")}
        loading={isLoading}
        onInstall={install}
      />

      {errMsg && <ErrorHelp error={errMsg} onRefresh={onSuccess} />}
    </>
  );
};
