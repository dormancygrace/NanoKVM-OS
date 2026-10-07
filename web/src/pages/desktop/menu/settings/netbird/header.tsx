import { useState } from "react";
import { Popconfirm, Popover } from "antd";
import {
  CircleStopIcon,
  EllipsisIcon,
  LoaderCircleIcon,
  RotateCwIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import * as api from "@/api/extensions/netbird.ts";
import { IconButton } from "@/components/ui/settings.tsx";

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
        <span />
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
                <IconButton
                  label={t("settings.netbird.restartAction")}
                  className="text-green-500"
                  icon={
                    loading === "restarting" ? (
                      <LoaderCircleIcon className="animate-spin" size={16} />
                    ) : (
                      <RotateCwIcon size={16} />
                    )
                  }
                />
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
                <IconButton
                  label={t("settings.netbird.stopAction")}
                  className="text-red-500"
                  icon={
                    loading === "stopping" ? (
                      <LoaderCircleIcon className="animate-spin" size={16} />
                    ) : (
                      <CircleStopIcon size={16} />
                    )
                  }
                />
              </Popconfirm>
              <Popover
                content={<Uninstall onSuccess={onSuccess} />}
                placement="bottomRight"
                arrow={false}
                trigger="click"
              >
                <IconButton
                  label={t("settings.netbird.moreActions")}
                  className="text-neutral-300"
                  icon={<EllipsisIcon size={16} />}
                />
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
