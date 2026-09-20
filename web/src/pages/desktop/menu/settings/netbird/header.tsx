import { useState } from "react";
import { Popconfirm, Popover } from "antd";
import {
  CircleStopIcon,
  EllipsisIcon,
  LoaderIcon,
  RotateCwIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import * as api from "@/api/extensions/netbird.ts";

import { ErrorHelp } from "./error-help.tsx";
import type { State } from "./types.ts";
import { Uninstall } from "./uninstall.tsx";

type HeaderProps = {
  state: State | undefined;
  onSuccess: () => void;
};

type Loading = "" | "restarting" | "stopping";

export const Header = ({ state, onSuccess }: HeaderProps) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState<Loading>("");
  const [errMsg, setErrMsg] = useState("");
  const hasKnownInstalledState = !!state && state !== "notInstall";

  function restart() {
    if (loading) return;
    setLoading("restarting");
    setErrMsg("");
    api
      .restart()
      .then((rsp) => {
        if (rsp.code !== 0) setErrMsg(rsp.msg);
      })
      .catch((err) =>
        setErrMsg(err?.message || t("settings.netbird.error.restartFailed")),
      )
      .finally(() => {
        setLoading("");
        onSuccess();
      });
  }

  function stop() {
    if (loading) return;
    setLoading("stopping");
    setErrMsg("");
    api
      .stop()
      .then((rsp) => {
        if (rsp.code !== 0) setErrMsg(rsp.msg);
      })
      .catch((err) =>
        setErrMsg(err?.message || t("settings.netbird.error.stopFailed")),
      )
      .finally(() => {
        setLoading("");
        onSuccess();
      });
  }

  return (
    <>
      <div className="flex items-center justify-between">
        <span className="text-base">{t("settings.netbird.title")}</span>
        <div className="flex items-center space-x-2">
          {hasKnownInstalledState && (
            <>
              <Popconfirm
                title={t("settings.netbird.restart")}
                onConfirm={restart}
                okText={t("settings.netbird.okBtn")}
                cancelText={t("settings.netbird.cancelBtn")}
                placement="bottom"
                disabled={!!loading}
              >
                <div className="flex cursor-pointer rounded p-1 text-green-500 hover:bg-neutral-600">
                  {loading === "restarting" ? (
                    <LoaderIcon className="animate-spin" size={18} />
                  ) : (
                    <RotateCwIcon size={18} />
                  )}
                </div>
              </Popconfirm>
              <Popconfirm
                title={t("settings.netbird.stop")}
                description={t("settings.netbird.stopDesc")}
                onConfirm={stop}
                okText={t("settings.netbird.okBtn")}
                cancelText={t("settings.netbird.cancelBtn")}
                placement="bottom"
                disabled={!!loading}
              >
                <div className="flex cursor-pointer rounded p-1 text-red-500 hover:bg-neutral-600">
                  {loading === "stopping" ? (
                    <LoaderIcon className="animate-spin" size={18} />
                  ) : (
                    <CircleStopIcon size={18} />
                  )}
                </div>
              </Popconfirm>
              <Popover
                content={<Uninstall onSuccess={onSuccess} />}
                placement="bottomRight"
                arrow={false}
              >
                <div className="flex cursor-pointer rounded p-1 text-neutral-300 hover:bg-neutral-600">
                  <EllipsisIcon size={18} />
                </div>
              </Popover>
            </>
          )}
        </div>
      </div>
      {errMsg && (
        <ErrorHelp
          error={errMsg}
          onRefresh={onSuccess}
          canRestart={hasKnownInstalledState}
        />
      )}
    </>
  );
};
