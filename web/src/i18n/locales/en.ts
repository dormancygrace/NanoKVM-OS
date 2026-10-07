const en = {
  translation: {
    audio: {
      receiving: 'Audio packets received',
      waiting: 'Waiting for audio',
      errors: {
        connection: 'Audio connection lost. Enable Listen to reconnect.',
        timeout: 'Audio connection timed out. Check the network and retry.',
        playback: 'Browser could not start audio playback. Tap Listen to retry.',
        signaling: 'Could not negotiate the audio connection. Reload the page and retry.',
        capture: 'USB audio capture stopped. Enable Listen to retry.',
        session: 'Your session expired. Sign in again.',
        closed: 'Audio connection closed. Enable Listen to reconnect.'
      },
      title: 'USB audio',
      listen: 'Listen',
      resume: 'Resume audio',
      volume: 'Volume',
      failed: 'Audio unavailable. Check the USB connection and try again.'
    },

    inputConnection: {
      idle: 'Control channel idle',
      connecting: 'Connecting to control channel',
      connected: 'Control channel connected',
      reconnecting: 'Control connection lost. Reconnecting…',
      disconnected: 'Control unavailable. Reconnecting… Video may still work.'
    },

    sessionControl: {
      active: 'This browser session controls keyboard and mouse',
      locked: 'Input is disabled in this session. Select Take control to enable it.',
      viewOnly: 'View only',
      take: 'Take control',
      release: 'Lock this session'
    },

    dateTime: {
      title: 'Date & time',
      deviceTime: 'Device time',
      timezone: 'Time zone',
      format: 'Time format',
      hour24: '24 hours (14:30)',
      hour12: '12 hours (2:30 PM)',
      preview: 'Preview',
      servers: 'NTP servers',
      serversHelp:
        'Choose or enter 1–6 hostnames or IP addresses. Changing servers restarts time synchronization.',
      scope:
        'Time zone applies to the device. The time format is shared across the interface, including VPN handshake times.',
      synchronized: 'Synchronized',
      waiting: 'Synchronization not confirmed',
      save: 'Save',
      saved: 'Date and time settings saved',
      loadFailed: 'Cannot read device time. Check the connection.',
      saveFailed: 'Could not apply settings. Check server addresses and the device connection.'
    },
    recorder: {
      title: 'Video recording',
      start: 'Start recording',
      stop: 'Stop recording',
      saving: 'Saving recording…',
      saved: 'Recording saved',
      unsupported:
        'Recording requires HTTPS and a browser with file saving support, such as Chrome or Edge',
      captureDisabled: 'Enable capture to record video',
      noVideo: 'Wait for video before recording',
      failed: 'Recording could not be saved. Check free disk space and browser support.'
    },
    screenshot: {
      take: 'Take screenshot',
      saving: 'Saving screenshot…',
      saved: 'Screenshot saved ({{width}}×{{height}})',
      captureDisabled: 'Enable capture to take a screenshot',
      noVideo: 'Wait for a video frame before taking a screenshot',
      failed: 'Screenshot could not be saved'
    },
    vpn: {
      rename: 'Rename',
      profileName: 'Profile name',
      cancelRename: 'Cancel renaming',
      openvpnDescription:
        'Import .ovpn profiles. Select any referenced certificate and key files in the same upload.',
      openvpnImport: 'Import OpenVPN files',
      openvpnEmpty: 'No OpenVPN profiles',
      openvpnLimit: 'Maximum 16 profiles; 256 KiB per file and combined profile.',
      openvpnRequired: 'OpenVPN is not installed in this system image.',
      openvpnNotInstalled: 'OpenVPN is not installed.',
      openvpnInstallDescription: 'Install OpenVPN on demand. Existing profiles are preserved.',
      openvpnInstall: 'Install OpenVPN',
      openvpnInstalled: 'OpenVPN is installed',
      openvpnUninstall: 'Uninstall OpenVPN',
      openvpnUninstallWarning:
        'OpenVPN will be stopped and removed. Existing profiles and settings remain on the device.',
      confirm: 'Yes',
      cancel: 'No',
      openvpnNote:
        'One OpenVPN profile can be enabled at a time. Enabled profiles reconnect after a restart. Routed TUN profiles are supported; TAP, scripts and interactive SSO/MFA are not. Server DNS applies to the whole device while connected.',
      credentials: 'Credentials',
      credentialsRequired: 'Credentials required',
      username: 'Username',
      password: 'Password',
      passphrase: 'Private-key passphrase',
      save: 'Save',

      description:
        'Import WireGuard profiles and enable the one you need. The enabled profile reconnects after a restart.',
      import: 'Import .conf files',
      empty: 'No WireGuard profiles',
      enable: 'Enable',
      delete: 'Delete',
      deleteConfirm: 'Delete this profile?',
      handshake: 'Handshake',
      loadFailed: 'Could not read VPN status.',
      requestFailed:
        'Connection interrupted. Reconnect to NanoKVM and check the tunnel status before trying again.',
      uploadLimit: 'Maximum 16 profiles, 64 KiB per file.',
      systemRequired:
        'WireGuard system tools are missing. Install a system image with WireGuard support.',
      routingNote:
        'Only the interface subnet is routed by default. Enable Route allowed IPs to add the peer routes.',
      routeAllowedIPs: 'Route allowed IPs',
      routingHelp: 'Add routes from AllowedIPs, including a default route for 0.0.0.0/0 or ::/0.',
      routingDisableFirst: 'Disable this profile before changing routing.',
      note: 'One WireGuard profile can be enabled at a time. Import does not connect automatically. DNS accepts IP addresses; executable hooks and SaveConfig are not supported. Idle means the tunnel has no recent handshake, not necessarily a connection failure.',
      states: {
        connecting: 'Connecting',
        reconnecting: 'Reconnecting',
        authenticating: 'Authenticating',

        off: 'Off',
        waiting: 'Waiting for peer',
        connected: 'Connected',
        idle: 'Idle',
        error: 'Error'
      }
    },

    dashboard: {
      coreCount_one: '{{count}} core',
      coreCount_other: '{{count}} cores',
      title: 'Dashboard',
      live: 'Updates while this page is open',
      stale: 'Some information could not be refreshed. Showing the last available values.',
      duration: '{{days}}d {{hours}}h {{minutes}}m',
      open: 'Open {{name}} settings',
      of: 'of {{total}}',
      freeStorage: 'Free space',
      uptime: 'Uptime',
      device: 'Device',
      application: 'NanoKVM OS',
      kernel: 'Kernel',
      processor: 'Processor',
      cores: 'cores',
      load: 'Load average · 1 / 5 / 15 min',
      capture: 'Capture',
      fps: 'Output / requested',
      sessions: 'Video sessions',
      memory: 'Memory',
      available: 'Available RAM',
      cache: 'Cache',
      zram: 'ZRAM',
      sdSwap: 'SD swap',
      swapUsage: '{{used}} / {{size}} MiB',
      compression: 'Compression',
      storage: 'Storage',
      systemStorage: 'System',
      dataStorage: 'Data',
      bootStorage: 'Boot',
      readOnly: 'Read-only',
      diskSpace: '{{free}} free of {{total}}',
      unavailable: 'Not mounted or unavailable',
      connected: 'Connected',
      disconnected: 'No connection',
      deviceTime: 'Device time',
      timezone: 'Time zone',
      synchronization: 'Synchronization',
      synchronized: 'Synchronized',
      notSynchronized: 'Synchronization not confirmed',
      timeService: 'Time service',
      ntpServers: 'NTP servers',
      handshake: 'Last handshake',
      noProfiles: 'No profiles',
      states: {
        off: 'Off',
        waiting: 'Waiting',
        connected: 'Connected',
        idle: 'Idle',
        error: 'Error',
        running: 'Connected',
        stopped: 'Stopped',
        notInstall: 'Not installed',
        notRunning: 'Stopped',
        notLogin: 'Not signed in',
        connecting: 'Connecting',
        auth: 'Authentication required'
      }
    },
    videoSettings: {
      hz: '{{value}} Hz',
      upToHz: 'up to {{value}} Hz',
      fpsValue: '{{value}} fps',
      mbps: '{{value}} Mbit/s',
      landscape: 'Landscape monitor',
      refreshFollows: 'Refresh follows the frame rate: {{value}} Hz',
      transport: 'Transport',
      transportHint: {
        direct: 'H.264/H.265 over HTTPS, decoded by the browser. Lowest latency.',
        webrtc: 'Works in most browsers and across networks; encrypted with SRTP.',
        mjpeg: 'A JPEG image per frame: the most compatible, the most traffic.'
      },
      streamSize: 'Encoded at {{value}}',
      fpsDelivered: 'Delivered: up to {{fps}} fps at {{size}}',
      bitrateHint: 'With motion (video, scrolling) about {{value}} Mbit/s fits this size and rate.',
      manual: 'Manual settings',
      userHint:
        'An administrator sets the shared video settings. You can choose how this browser receives and plays the video.',
      invalid: 'Some selected options are unavailable',
      changeMonitor:
        'The monitor switches to {{monitor}}: the connected computer detects it again and the picture blinks.',
      changeReload: 'The page reloads to switch the player.',
      preset: {
        title: 'Profile',
        custom: 'Custom',
        auto: 'Recommended',
        autoHint: '1920 × 1080 up to 100 fps, bitrate for motion',
        sharp: 'Sharpest',
        sharpHint: 'Largest monitor: 4K at 30 or 2K at 60 fps, 20 Mbit/s',
        balanced: 'Balanced',
        balancedHint: '2560 × 1440 at 60 fps, bitrate for motion',
        responsive: 'Lowest latency',
        responsiveHint: '1280 × 720 at 120 fps without a playback buffer',
        compatible: 'Compatible',
        compatibleHint: 'WebRTC H.264, 1080p60, any browser',
        saver: 'Low traffic',
        saverHint: '1080p at 30 fps, 1 Mbit/s; the computer renders 30 Hz'
      },
      reason: {
        'video-memory': 'needs the 4K video memory mode (Settings → Memory, restart)',
        receiver: 'not supported by this HDMI receiver',
        browser: 'this browser cannot play it',
        codec: 'not available with this transport',
        transport: 'not available with this transport',
        portrait: 'not supported by this portrait profile',
        range: 'out of range'
      },
      mjpegChroma: 'JPEG color sampling',
      mjpegChroma420: '4:2:0 — smaller frames',
      mjpegChroma422: '4:2:2 — sharper color edges',
      mjpegChromaHint:
        '4:2:2 preserves more color detail and can increase frame size. The choice is saved and applies without a restart.',
      mjpegChromaActive: 'Active JPEG color sampling',
      mjpegChromaFallback_resolution:
        'MJPEG is limited to 1920 pixels in width to preserve 4:2:2 while H.264/H.265 uses a wider output. The selected resolution resumes when that video stream ends.',
      mjpegChromaFallback_video:
        'Only one capture output supports widths above 1920 pixels. MJPEG uses 4:2:0 while both streams are wider; 4:2:2 resumes when the video stream ends or either stream is reduced.',
      mjpegChromaFallback_frameDetection: 'Turn off frame detection to use 4:2:2.',
      mjpegChromaFallback_diagnostic: '4:2:2 is unavailable in the current capture mode.',
      mjpegChromaFallback_hardware:
        'Capture fell back to 4:2:0 after an error. Retry 4:2:2 when the signal is stable.',
      mjpegChromaFallback_pending: '4:2:2 will apply to the next captured frame.',
      cubeMonitorHint:
        'Cube/Lite: writes and verifies the EDID. Physically disconnect all power sources and reconnect to apply it. A software reboot is not sufficient. Automatic uses the conservative 1080p/60 profile.',
      powerCycleTitle: 'Physical power cycle required',
      powerCycleConfirm:
        'Only continue if you can physically disconnect all power sources from the NanoKVM after writing. Video may be interrupted until then. Rebooting through software does not apply this change.',
      powerCycleWrite: 'Write EDID',
      powerCycleCancel: 'Cancel',
      powerCycleWritten:
        'EDID written and verified. Disconnect and reconnect NanoKVM power to apply.',
      powerCyclePending: 'EDID maintenance: power cycle required',
      powerCyclePendingHint:
        'An EDID write was attempted. Disconnect all power sources and reconnect the NanoKVM. This reminder survives software reboots; dismiss it only after the physical power cycle.',
      powerCycleAck: 'I have power-cycled the device',
      powerCycleAckConfirm:
        'Confirm that you physically disconnected all power sources and reconnected the NanoKVM. This only dismisses the reminder; it does not restart the device.',

      directPlayback: 'Direct playback',
      directSmooth: 'Smooth picture',
      directImmediate: 'Lowest latency',
      directSmoothHint:
        'Adaptively buffers frames to smooth uneven delivery, adding a small delay.',
      directImmediateHint:
        'Displays frames as soon as they are decoded. Uneven delivery can cause visible stutter.',
      directPlaybackLocal: 'Saved in this browser. Applies to H.264 and H.265 Direct.',

      apply: 'Apply',
      discard: 'Discard changes',
      pending: 'Changes have not been applied',
      applied: 'Video settings applied',
      title: 'Video',
      open: 'Video settings…',
      description: 'Configure the HDMI monitor independently from the video sent to your browser.',
      sameAsInput: 'Same as input',
      atMost: 'Up to {{value}}',
      streamResolution: 'Stream resolution',
      failed: 'Could not apply video settings.',
      bitrate: 'Bitrate',
      current: 'Current video',
      captureOn: 'Capture enabled',
      captureOff: 'Capture disabled',
      input: 'HDMI input',
      output: 'Encoded stream',
      monitor: 'HDMI monitor',
      monitorProfile: 'Virtual monitor profile',
      portrait: 'Portrait monitor',
      automatic: 'Automatic (recommended)',
      monitorHint:
        'Advertises a preferred mode and fallback timings. BIOS and the operating system may choose different resolutions; capture follows the actual signal automatically. Changing this profile briefly reconnects HDMI.',
      monitorUnavailable:
        'Changing the virtual monitor profile is unavailable on this device. Input detection remains automatic.',
      stream: 'Video stream',
      streamHint:
        'Resolution, frame rate and bitrate affect all viewers. Changing stream resolution does not change the computer’s desktop. New viewers automatically use the active encoder codec; transport and display scale remain individual.',
      advanced: 'Advanced and recovery',
      gopMode: 'H.265 GOP mode',
      gopModeHint:
        'NormalP uses less video memory. SmartP can reduce traffic on static scenes but keeps an additional background-reference frame. The saved selection takes effect after a device reboot.',
      gopModeRebootRequired: 'GOP mode saved. Reboot the device to apply it.',
      gopModePendingReboot: 'A saved H.265 GOP mode change is waiting for a device reboot.',
      gopModePendingRebootHint: 'Active: {{active}}. Selected: {{selected}}.',
      gopHint:
        'GOP is the interval between keyframes. HDMI recovery restarts capture if the source stops responding.',
      fpsLimited:
        'The current input limits capture to {{fps}} fps. The saved request is kept for the next source mode.',
      statusFailed: 'Could not refresh video status.',
      retry: 'Retry'
    },
    capture: {
      downloadStatusFailed: 'Could not refresh download status. Reconnecting…',
      start: 'Enable capture',
      stop: 'Disable capture',
      off: 'Capture disabled',
      checking: 'Checking device state…',
      failed: 'Could not change capture. Try again.',
      storageCheckFailed: 'Could not check image storage. Retry after reconnecting.',
      storageUnavailable: 'Image storage is not writable. Check free space and storage state.'
    },
    head: {
      desktop: 'Remote desktop',
      login: 'Sign in',
      changePassword: 'Change password',
      terminal: 'Terminal',
      wifi: 'Wi-Fi'
    },
    auth: {
      login: 'Sign in',
      placeholderUsername: 'Username',
      placeholderPassword: 'Password',
      placeholderCurrentPassword: 'Current password',
      placeholderPassword2: 'Please enter password again',
      noEmptyUsername: 'Username required',
      noEmptyPassword: 'Password required',
      passwordLength: 'Password must be between 8 and 72 characters',
      noAccount: 'Failed to get user information, please refresh web page or reset password',
      invalidUser: 'Invalid username or password',
      locked: 'Too many sign-in attempts, please try again later',
      globalLocked: 'System under protection, please try again later',
      error: 'Unexpected error',
      invalidCurrentPassword: 'Current password is incorrect',
      changePassword: 'Change password',
      changePasswordDesc: 'For the security of your device, please change the password!',
      passwordChanged: 'Password changed. Sign in with the new password.',
      cancelAndLogout: 'Cancel and log out',
      differentPassword: 'Passwords do not match',
      illegalUsername: 'Username contains illegal characters',
      illegalPassword: 'Password contains illegal characters',
      forgetPassword: 'Forgot password',
      ok: 'OK',
      cancel: 'Cancel',
      loginButtonText: 'Sign in',
      tips: {
        reset1:
          'To reset the passwords, press and hold the BOOT button on the NanoKVM for 10 seconds.',
        reset2: 'For detailed steps, please consult this document:',
        reset3: 'Web default account:',
        reset4: 'SSH default account:',
        change1: 'For the built-in admin account, this action changes the following passwords:',
        change2: 'Web login password',
        change3: 'System root password (SSH login password)',
        change4: 'To reset the passwords, press and hold the BOOT button on the NanoKVM.'
      }
    },
    wifi: {
      title: 'Wi-Fi',
      description: 'Configure Wi-Fi for NanoKVM',
      success: 'Please go to the device to check the network status of NanoKVM.',
      failed: 'Operation failed, please try again.',
      pending:
        'Connecting to the network. The NanoKVM hotspot may disconnect; check the network status on the device.',
      invalidMode:
        'The current mode does not support network setup. Please go to your device and enable Wi-Fi configuration mode.',
      confirmBtn: 'OK',
      finishBtn: 'Finished',
      ap: {
        authTitle: 'Authentication required',
        authDescription: 'Please enter the AP password to continue',
        authFailed: 'Invalid AP password',
        passPlaceholder: 'AP password',
        verifyBtn: 'Verify'
      }
    },
    screen: {
      monitorApplying: 'Changing HDMI monitor resolution… Video will reconnect.',
      monitorHint: 'Changes the HDMI monitor seen by your computer. Video briefly disconnects.',
      monitorFailed: 'Could not change the HDMI monitor resolution',
      scale: 'Scale',
      title: 'Screen',
      video: 'Video mode',
      codec: 'Codec',
      unsupported: 'unsupported',
      resolution: 'Resolution',
      controlRegion: {
        title: 'Mouse calibration',
        description:
          'Use this setting when the controlled device uses a non-16:9 resolution and the cursor is misaligned horizontally or vertically.',
        off: 'Off',
        auto: 'Auto',
        autoWarning: 'Calibration may fail when the user application has a pure black background.',
        manual: 'Manual',
        selectedResolution: 'Selected area resolution',
        unused: 'Not used',
        originalResolution: 'Original resolution',
        selectResolution: 'Select original resolution',
        addResolution: 'Add custom resolution',
        add: 'Add',
        duplicateResolution: 'This resolution already exists.',
        width: 'Width',
        height: 'Height',
        invalidResolution: 'Enter a valid original resolution after the video is ready.',
        select: 'Select area',
        saveFailed: 'Failed to save the input area.',
        tooSmall: 'The selected area is too small.',
        previewUnavailable: 'Preview unavailable',
        dragHint: 'Drag to select the remote desktop area',
        finish: 'Done',
        cancel: 'Cancel'
      },
      auto: 'Automatic',
      autoTips:
        'Restores the default monitor profile and lets the connected computer choose its resolution.',
      fps: 'Frame rate',
      customizeFps: 'Customize',
      quality: 'Quality',
      qualityLossless: 'Lossless',
      qualityHigh: 'High',
      qualityMedium: 'Medium',
      qualityLow: 'Low',
      frameDetect: 'Frame detection',
      frameDetectTip:
        "Calculate the difference between frames. Stop transmitting video stream when no changes are detected on the remote host's screen.",
      resetHdmi: 'Recover HDMI',
      encoderError: 'Video encoder error',
      encoderUnsupported: 'The selected codec is not supported by this browser in this mode.',
      activeEncoderUnsupported:
        'Another viewer is using {{codec}}, which this browser cannot play in the selected mode. Try another video mode or a compatible browser.',
      encoderStateFailed: 'Could not read the active stream settings. Retry to connect.',
      retryJoin: 'Retry',
      sessions: 'Active video sessions: {{count}}',
      encoderConflict:
        'Another viewer is using different encoder settings. Close it or select the same settings.',
      mixedH264: {
        title: 'H.264 stream conflict',
        description:
          'H.264 Direct and H.264 WebRTC are being used at the same time. This may cause screen tearing or corrupted video. Please use only one H.264 mode.'
      },
      webrtcConnectionFailed: {
        title: 'WebRTC connection failed',
        description: 'Check the network connection or switch the video mode.'
      },
      captureStatus: {
        hdmiError: 'HDMI screen error',
        unsupportedResolution: 'Current resolution is not supported',
        retrieving: 'Getting screen…',
        changingResolution: 'Switching resolution…',
        updateFailed: 'Screen cannot update right now',
        videoError: 'Video display error',
        noHdmi: 'No HDMI signal detected',
        unavailable: 'Screen cannot be displayed right now'
      }
    },
    keyboard: {
      title: 'Keyboard',
      paste: 'Paste',
      tips: 'Only standard keyboard letters and symbols are supported',
      placeholder: 'Please input',
      submit: 'Submit',
      virtual: 'Keyboard',
      readClipboard: 'Read from clipboard',
      clipboardPermissionDenied:
        'Clipboard permission denied. Please allow clipboard access in your browser.',
      clipboardReadError: 'Failed to read clipboard',
      dropdownEnglish: 'English',
      dropdownGerman: 'German',
      dropdownFrench: 'French',
      dropdownRussian: 'Russian',
      dropdownSpanish: 'Spanish',
      shortcut: {
        title: 'Shortcuts',
        custom: 'Custom',
        capture: 'Click here to capture shortcut',
        clear: 'Clear',
        save: 'Save',
        captureTips:
          'Capturing system-level keys (such as the Windows key) requires full-screen permission.',
        enterFullScreen: 'Toggle full-screen mode.'
      },
      leaderKey: {
        title: 'Leader key',
        desc: 'Bypass browser restrictions and send system shortcuts directly to the remote host.',
        howToUse: 'How to use',
        simultaneous: {
          title: 'Simultaneous mode',
          desc1: 'Press and hold the leader key, then press the shortcut.',
          desc2: 'Intuitive, but may conflict with system shortcuts.'
        },
        sequential: {
          title: 'Sequential mode',
          desc1:
            'Press the leader key → press the shortcut in sequence → press the leader key again.',
          desc2: 'Requires more steps, but completely avoids system conflicts.'
        },
        enable: 'Enable leader key',
        tip: 'When assigned as a leader key, this key functions exclusively as a shortcut trigger and loses its default behavior.',
        placeholder: 'Please press the leader key',
        shiftRight: 'Right Shift',
        ctrlRight: 'Right Ctrl',
        metaRight: 'Right Win',
        submit: 'Submit',
        recorder: {
          rec: 'REC',
          activate: 'Activate keys',
          input: 'Please press the shortcut…'
        }
      }
    },
    mouse: {
      title: 'Mouse',
      cursor: 'Cursor style',
      default: 'Default cursor',
      pointer: 'Pointer cursor',
      cell: 'Cell cursor',
      text: 'Text cursor',
      grab: 'Grab cursor',
      hide: 'Hide cursor',
      mode: 'Mouse mode',
      absolute: 'Absolute mode',
      relative: 'Relative mode',
      direction: 'Wheel direction',
      scrollUp: 'Scroll up',
      scrollDown: 'Scroll down',
      speed: 'Wheel speed',
      fast: 'Fast',
      slow: 'Slow',
      requestPointer: 'Using relative mode. Please click desktop to get mouse pointer.',
      inputAdapter: {
        title: 'Input adapter',
        auto: 'Auto',
        'pointer-lock': 'Pointer Lock',
        touchpad: 'Touchpad'
      },
      touchpadGuide: {
        title: 'Touchpad guide',
        scope: 'Applies when Input adapter is Touchpad and Mouse mode is Relative.',
        screen: 'Screen',
        swipeTitle: 'Swipe to move',
        swipeDesc: 'Swipe inside the active screen area to move the remote pointer.',
        tapTitle: 'Tap to click',
        tapDesc: 'A short tap sends one left click.',
        holdTitle: 'Hold for left button down',
        holdDesc: 'Touch and keep still for about 1 second to hold the left mouse button down.',
        dragTitle: 'Move after hold to drag',
        dragDesc: 'After the hold is active, move your finger to drag with the left button held.'
      },
      resetHid: 'Reset HID'
    },
    image: {
      remote: {
        device: 'From SD',
        browser: 'From this computer',
        hint: 'Mount an ISO from this computer as a read-only CD/DVD. Only requested blocks are transferred. Keep this tab open while using the image.',
        select: 'Select ISO file',
        connect: 'Connect ISO',
        connecting: 'Connecting…',
        mounted: 'Connected · Read-only',
        read: 'read',
        disconnect: 'Disconnect ISO',
        readFailed: 'Could not read the selected ISO. Reconnect the file to try again.',
        connectFailed: 'Could not connect the ISO. Check the device connection and try again.',
        disconnectFailed: 'Could not disconnect the ISO. Try again.'
      },
      title: 'Images',
      loading: 'Loading…',
      empty: 'Nothing found',
      mountMode: 'Mount mode',
      massStorage: 'Mass storage',
      cdrom: 'CD/DVD',
      mountFailed: 'Mount failed',
      mountDesc:
        'On some systems, you need to eject the virtual disk from the remote host before mounting the image.',
      unmountFailed: 'Unmount failed',
      unmountDesc:
        'On some systems, you need to manually eject from the remote host before unmounting the image.',
      forceEject: 'Force eject',
      forceEjectDesc:
        'The remote host is still using this image. Force eject may interrupt pending reads or writes. Continue?',
      refresh: 'Refresh the image list',
      attention: 'Attention',
      deleteConfirm: 'Are you sure you want to delete this image?',
      okBtn: 'Yes',
      cancelBtn: 'No',
      tips: {
        title: 'How to upload',
        usb1: 'Connect the NanoKVM to your computer via USB.',
        usb2: 'Ensure that the virtual disk is mounted (Settings - Virtual disk).',
        usb3: 'Open the virtual disk on your computer and copy the image file to the root directory of the virtual disk.',
        scp1: 'Make sure the NanoKVM and your computer are on the same local network.',
        scp2: 'Open a terminal on your computer and use the SCP command to upload the image file to the /data directory on the NanoKVM.',
        scp3: 'Example: scp your-image-path root@your-nanokvm-ip:/data',
        tfCard: 'TF card',
        tf1: 'This method is supported on Linux system',
        tf2: 'Get TF card from the NanoKVM (for the FULL version, disassemble the case first).',
        tf3: 'Insert the TF card into a card reader and connect it to your computer.',
        tf4: 'Copy the image file to the /data directory on the TF card.',
        tf5: 'Insert the TF card into the NanoKVM.'
      }
    },
    script: {
      title: 'Scripts',
      upload: 'Upload',
      run: 'Run',
      runBackground: 'Run in background',
      runFailed: 'Run failed',
      attention: 'Attention',
      delDesc: 'Are you sure you want to delete this file?',
      confirm: 'Yes',
      cancel: 'No',
      delete: 'Delete',
      close: 'Close'
    },
    terminal: {
      title: 'Terminal',
      nanokvm: 'NanoKVM terminal',
      usbSerial: 'USB serial console',
      usbSpeed: 'Virtual USB serial — baud rate does not limit transfer speed.',
      invalidParameters: 'Invalid serial parameters.',
      disconnected: 'Connection closed. Press Enter to reconnect.',
      serial: 'Serial port terminal',
      serialPort: 'Serial port',
      serialPortPlaceholder: 'Please enter the serial port',
      baudrate: 'Baud rate',
      parity: 'Parity',
      parityNone: 'None',
      parityEven: 'Even',
      parityOdd: 'Odd',
      flowControl: 'Flow control',
      flowControlNone: 'None',
      flowControlSoft: 'Soft',
      flowControlHard: 'Hard',
      dataBits: 'Data bits',
      stopBits: 'Stop bits',
      confirm: 'OK'
    },
    wol: {
      title: 'Wake-on-LAN',
      sending: 'Sending command…',
      sent: 'Magic packet sent. The target may still be off or unavailable.',
      input: 'Please enter the MAC',
      interface: 'Interface',
      ok: 'OK',
      name: 'Device name',
      save: 'Save name',
      cancel: 'Cancel',
      send: 'Send Wake-on-LAN',
      remove: 'Remove from history'
    },
    download: {
      progress: 'Transfer progress',
      downloading: 'Downloading',
      uploading: 'Uploading',
      finishing: 'Finishing…',
      title: 'Image downloader',
      input: 'Please enter a remote image URL',
      ok: 'OK',
      disabled: '/data partition is RO, so we cannot download the image',
      uploadbox: 'Drop file here or click to select',
      inputfile: 'Please enter the image file',
      NoISO: 'No ISO',
      sha256: 'SHA-256 (optional)',
      sha256Placeholder: 'Enter a 64-character SHA-256 checksum',
      invalidSHA256: 'SHA-256 must be a 64-character hexadecimal string',
      failed: 'Download failed',
      success: 'Download successful',
      checksumFailed: 'Download failed: SHA-256 verification failed',
      cancel: 'Cancel',
      cancelFailed: 'Failed to cancel download'
    },
    power: {
      title: 'Power',
      showConfirm: 'Confirmation',
      showConfirmTip: 'Power operations require an extra confirmation',
      reset: 'Reset',
      power: 'Power',
      powerShort: 'Power (short press)',
      powerLong: 'Power (long press)',
      hddLed: 'HDD LED',
      resetConfirm: 'Proceed reset operation?',
      powerConfirm: 'Proceed power operation?',
      okBtn: 'Yes',
      cancelBtn: 'No',
      controlRequired: 'Another session controls the input. Take control first.'
    },
    settings: {
      software: {
        addons: {
          title: 'Add-ons',
          packages: 'Packages',
          open: 'Open PicoClaw',
          source: 'Official releases',
          picoclawDescription:
            'AI assistant with optional remote control. Installs the latest stable release from the official PicoClaw website.'
        },
        title: 'Software',
        description:
          'Manage packages from the configured Alpine repositories. Package changes use the same APK database and services as SSH.',
        refresh: 'Refresh indexes',
        upgrade: 'Upgrade all',
        upgradeConfirm: 'Upgrade all installed packages? Video and control may briefly disconnect.',
        available: 'Available packages',
        updatesTab: 'Updates',
        updateAvailable: 'Packages with available updates',
        upToDate: 'All installed packages are up to date.',
        searchPlaceholder: 'Search package name',
        search: 'Search',
        searchHint: 'Search starts automatically after you pause typing.',
        noResults: 'No matching packages',
        indexMissing:
          'No package indexes are available. Refresh indexes before searching or installing.',
        indexStale:
          'Package indexes may be outdated (last refreshed {{updated}}). Refresh before installing.',
        indexUnknown: 'unknown time',
        install: 'Install',
        installed: 'Installed packages',
        remove: 'Remove',
        removeTitle: 'Remove {{package}}?',
        removeDescription:
          'APK will perform this removal and its dependency changes exactly as shown below. This cannot be undone automatically.',
        started: 'APK operation started',
        running: 'APK is {{action}} {{package}}',
        succeeded: 'APK operation completed',
        failed: 'APK operation failed',
        requestFailed: 'The software request failed. Check device connectivity and retry.'
      },
      updates: {
        apk: {
          title: 'Package updates',
          description:
            'Update installed packages from the configured repositories. Settings are kept. Services restart automatically; a kernel update requires a reboot.',
          kernel: 'Running kernel',
          check: 'Check for updates',
          install: 'Install updates',
          confirm: 'Install these package updates? Video and control may briefly disconnect.',
          installed: 'Package updates installed',
          current: 'All installed packages are up to date',
          failed: 'Package update failed',
          rebootRequired:
            'Updates are installed. Restart the device to use the new kernel or system components.',
          reboot: 'Restart device',
          rebootConfirm: 'Restart the device now?',
          log: 'Update log'
        },
        title: 'Updates',
        description:
          'Update NanoKVM OS using signed packages. System updates restart the device; application updates briefly disconnect video and control.',
        installed: 'Installed',
        alpineOptions: 'Image options',
        alpineTitle: 'Reinstall system',
        alpineDisclaimer:
          'Replace the system with a newly built image. Saved settings are restored and selected packages are included. Other files on the system partition are removed. The device will restart.',
        alpineProfile: 'Build profile',
        alpinePackages: 'Packages to include',
        alpineBuild: 'Build image',
        alpineStage: 'Download image',
        alpineInstall: 'Reinstall and restart',
        alpineConfirm: 'Format the system partition, install this Alpine image, and reboot now?',
        alpineBuilderMissing:
          'The attended image builder is not configured. Set alpine.builderURL or use a manually verified recovery bundle.',
        requestFailed: 'The update request failed. Check device connectivity and retry.',
        reconnecting: 'Waiting for the device to reconnect…'
      },
      memory: {
        videoMode: 'Video memory allocation',
        videoModeDescription:
          'CMA lends unused video memory to Linux. The fixed modes reserve it for video only. 3840 × 2160 needs the 4K mode, because with CMA the encoder cannot always get its memory back. Changes apply after reboot.',
        video_cma: 'CMA — 128 MiB, up to 2560 × 1440 (default)',
        video_fixed: 'Fixed — 64 MiB, up to 2560 × 1440',
        video_uhd: '4K — 128 MiB fixed, up to 3840 × 2160',
        zramAuto: 'Half of RAM ({{size}} MiB)',
        videoActive: 'Currently active',
        videoUnknown: 'Unknown',
        videoModeUnavailable: 'Update the kernel package to enable switching.',
        videoReboot: 'Restart the device to apply the selected video memory mode.',

        recompressTitle: 'Recompress cold pages with ZSTD',
        recompressDescription:
          'Keep fast LZ4 for new pages. Every 15 seconds, try up to 1 MiB of pages untouched for a minute. Uses extra CPU and compressor memory; reading these pages may be slower. Changing this mode recreates ZRAM and requires enough free RAM.',
        recompressNotReady:
          'The saved mode is not active. Reapply it when sufficient RAM is available.',
        applicationTitle: 'Reduce application memory',
        title: 'Memory',
        ram: 'RAM in use',
        available: 'Available',
        cache: 'Reclaimable cache',
        video: 'Video buffers (ION)',
        ramNote:
          'Available memory includes reclaimable cache. Video buffers use a separate reserved region.',
        zramTitle: 'Enable ZRAM swap',
        sdTitle: 'Enable SD swap',
        zramDescription:
          'Compress inactive memory pages in RAM. Capacity is measured before compression; RAM is allocated as needed.',
        sdDescription:
          'Use a swap file on the SD card when memory is scarce. SD access is slower than RAM.',
        size: 'Capacity',
        used: 'Used',
        algorithm: 'Compression',
        actualRam: 'Page storage in RAM',
        priorityNote:
          'ZRAM is used before SD swap. Changes apply immediately and persist after reboot.',
        unavailable: 'Requires an Enhanced firmware build with this feature.',
        loadError: 'Could not read memory status',
        changeError: 'Could not change swap settings'
      },
      system: {
        title: 'System',
        general: 'General',
        logs: {
          title: 'Logs',
          description:
            'Up to 1 MiB per source. Archives from the last two device boots are saved once a minute and on normal server shutdown.',
          source: 'Log source',
          sources: {
            system: 'System and services',
            kernel: 'Kernel',
            application: 'NanoKVM'
          },
          boot: 'Device boot',
          boots: {
            current: 'Current boot',
            previous: 'Previous boot',
            older: 'Two boots ago'
          },
          search: 'Search this log',
          onlyMatches: 'Only matching lines',
          previousMatch: 'Previous matching line',
          nextMatch: 'Next matching line',
          refresh: 'Refresh',
          follow: 'Auto-refresh (5 s)',
          loading: 'Loading logs…',
          empty: 'No log entries to display.',
          unavailable: 'This log source is unavailable for the selected boot.',
          archiveUnavailable:
            'Persistent log archives are unavailable. Current logs can still be viewed.',
          noHistory: 'Earlier boots will become available after the next device reboot.',
          loadError: 'Could not read logs.',
          collected: 'Read at {{time}} · {{count}} lines',
          truncated: 'Earlier or oversized entries were omitted.',
          privacy:
            'Entries with recognized credentials are hidden. Logs may still contain device and network details.'
        },
        diagnostics: {
          overview: 'Status',
          title: 'Diagnostics',
          description:
            'Read-only support status. Collection is cached and never changes the device.',
          refresh: 'Refresh',
          download: 'Download report',
          loadError: 'Could not collect diagnostics',
          downloadError: 'Could not download the diagnostic report',
          collected: 'Collected: {{time}}',
          versions: 'Versions and compatibility',
          application: 'Application',
          image: 'Image',
          alpine: 'Alpine',
          kernel: 'Running kernel',
          systemBase: 'System base',
          modules: 'Kernel modules',
          boot: 'Boot components',
          video: 'HDMI and video capture',
          capture: 'Capture',
          edid: 'EDID programming',
          operationFailed:
            'The latest operation failed; the report intentionally omits its raw log.',
          profile: 'EDID profile',
          input: 'Input',
          output: 'Output',
          fps: 'Measured fps',
          usb: 'USB gadget',
          binding: 'Controller binding',
          selected: 'Selected functions',
          firewall: 'nftables',
          services: 'OpenRC services',
          optional: 'optional',
          apk: 'APK',
          states: {
            ok: 'OK',
            running: 'running',
            stopped: 'stopped',
            absent: 'not installed',
            unavailable: 'unavailable',
            error: 'error',
            disabled: 'disabled',
            'no-signal': 'no signal',
            checking: 'checking',
            installing: 'installing',
            ready: 'ready',
            'up-to-date': 'up to date',
            installed: 'installed',
            configured: 'configured',
            'saved-profile': 'saved profile',
            'power-cycle-pending': 'physical power cycle pending',
            'pending-reboot': 'reboot pending'
          }
        },
        services: 'Services',
        device: 'Device',
        performance: 'Performance',
        ssh: {
          description: 'Enable SSH remote access',
          tip: 'SSH is enabled only after you set a new password for the Linux root account.',
          passwordTitle: 'Set SSH root password',
          passwordDescription:
            'Set a strong password for the Linux root account before enabling SSH. It is used to sign in over SSH.',
          password: 'New root password',
          confirmation: 'Confirm root password',
          rootForbidden: 'The password cannot be “root”.',
          enable: 'Set password and enable SSH',
          failed: 'Could not set the root password or enable SSH.'
        }
      },
      title: 'Settings',
      back: 'Back',
      close: 'Close',
      mcp: {
        title: 'MCP service',
        service: 'Remote control MCP',
        serviceDesc:
          'Allow trusted MCP clients to control the keyboard and mouse and capture screenshots',
        securityWarning:
          'Anyone with this API key can control the remote host and view its screen. Use HTTPS and enable it only on trusted networks.',
        endpoint: 'Endpoint',
        apiKey: 'API key',
        showKey: 'Show API key',
        hideKey: 'Hide API key',
        regenerateKey: 'Regenerate API key',
        copyValue: 'Copy {{label}}',
        regenerateConfirmTitle: 'Regenerate MCP API key?',
        regenerateConfirmDesc: 'The current key will stop working immediately.',
        enableConfirmTitle: 'Enable external MCP control?',
        enableConfirmDesc: 'Enabling MCP will stop PicoClaw and close any active PicoClaw session.',
        failed: 'MCP operation failed',
        copyFailed: 'Copy failed. Copy manually.',
        okBtn: 'Confirm',
        cancelBtn: 'Cancel'
      },
      about: {
        title: 'About',
        description: 'Community firmware for NanoKVM.',
        specialThanksTitle: 'Special thanks',
        specialThanksWife:
          'To my wife — for her patience, support, and for enduring my absence during the most intensive phase of development.',
        credits: {
          button: 'Credits',
          title: 'Credits',
          pause: 'Pause',
          resume: 'Resume',
          close: 'Close',
          madePossibleBy: 'Made possible by',
          projectLead:
            'Concept, architecture, NanoKVM OS development, system integration, device validation, and release engineering.',
          wenjie:
            'Original NanoKVM application stewardship; Direct H.264, Tailscale startup and networking fixes; and UI and localization integration.',
          z2zGuGu:
            'Native KVM, MMF and vision components; build tooling, watchdog, EDID and USB PID/VID tools; and device customization foundations.',
          alexanderGerReich:
            'Application, update and download flows; HID, paste and virtual keyboard improvements; and German and English UI maintenance.',
          andrewMoryakov:
            'NetBird client and settings integration, hardened VPN lifecycle and recovery handling, artifact verification, and regression CI.',
          watermeko:
            'Verified and cancellable downloads and OTA, low-latency and zero-copy H.264 work, configurable input regions, and mouse fixes.',
          scpcom:
            'Virtual-media, download and storage reliability; HID locking; hostname and network fixes; and native MMF cleanup.',
          lingkongSky:
            'Mouse Jiggler, hostname and web-title editing, named Wake-on-LAN entries, power confirmations, and swap controls.',
          polyzium:
            'Russian localization, the generic 104-key virtual keyboard, fit-to-window behavior, and UI translation fixes.',
          yuryPekishev:
            'Russian virtual-keyboard layout and Russian-to-English conversion for pasted keyboard input.',
          thomasPressnell:
            'Persistent HDMI enable and disable support, and separation of USB gadget, data attachment, and virtual-media controls.',
          s33g: 'Login branding and text, toolbar tooltips, localizable errors, and device-free development mocks.',
          gxcreator:
            'Detailed issue reports and persistent feedback that exposed regressions and directly shaped fixes.',
          rockymtngeek:
            'LT6911D hardware testing, detailed diagnostic logs, and confirmation of the capture fix.',
          yuziCo:
            'MJPEG duplicate suppression and capture backpressure, plus broadcast acquisition and DHCP option 121 designs adapted from IronKVM.',
          sipeed: 'The original NanoKVM hardware platform and upstream application foundation.',
          silicon: 'The SG2002 media platform, SDK foundations, and low-level hardware support.',
          systemStack:
            'The operating system, build infrastructure, package management, and countless upstream fixes.',
          appStack:
            'The application, browser interface, secure transport, and real-time media building blocks.',
          projects:
            'Ideas, prior art, interoperability work, and inspiration across the open KVM community.',
          contributorsTitle: 'Open-source contributors',
          contributors:
            'Code, translations, testing, issue reports, reviews, documentation, and patient feedback.',
          closing: 'Thank you for making NanoKVM OS possible.'
        },
        documentation: 'Documentation',
        reportIssue: 'Report an issue',
        upstreamCredit:
          'Built on the original Sipeed NanoKVM project. Thank you to its authors and contributors.',
        ip: 'IP',
        image: 'Image version',
        hostname: 'Hostname',
        hostnameEdit: 'Edit hostname',
        hostnameSave: 'Save hostname',
        hostnameCancel: 'Cancel editing',
        hostnameUpdated: 'Hostname updated. Reboot to apply.'
      },
      appearance: {
        branding: {
          title: 'Branding',
          description:
            'Customize the login logo and browser favicon independently. Built-in images remain available as defaults.',
          logoTitle: 'Login logo',
          logoDescription: 'The main logo shown above the sign-in form.',
          logoPreview: 'Login logo preview',
          logoUpload: 'Upload login logo',
          faviconTitle: 'Favicon',
          faviconDescription: 'The small icon shown in the browser tab.',
          faviconPreview: 'Favicon preview',
          faviconUpload: 'Upload favicon',
          defaultActive: 'Built-in default',
          customActive: 'Custom image',
          restoreDefault: 'Restore default',
          formats:
            'PNG or JPEG, up to 2 MiB and 1024 × 1024 pixels. A square transparent PNG works best.',
          failed: 'Could not save branding.'
        },
        buttonColor: {
          title: 'Button color',
          description: 'Choose the accent color used by primary buttons throughout the interface.',
          accentTitle: 'Primary buttons',
          accentDescription: 'Hover and pressed states are generated automatically.',
          preview: 'Preview button',
          inputLabel: 'Button color in hexadecimal format',
          defaultActive: 'Built-in logo green (#45E9A0)',
          customActive: 'Custom color',
          save: 'Apply',
          restoreDefault: 'Restore default',
          failed: 'Could not save the button color.'
        },
        bannerStyle: {
          title: 'SSH banner',
          description: 'Choose the banner shown when a new interactive SSH session starts.',
          default: 'Default',
          rainbow: 'Rainbow',
          failed: 'Could not save the SSH banner style.'
        },
        title: 'Appearance',
        display: 'Display',
        customize: 'Customize',
        language: 'Language',
        languageDesc: 'Select the language for the interface',
        languagePartial: 'partial',
        languagePartialHint: 'Partially translated. Missing texts are shown in English.',
        webTitle: 'Web title',
        webTitleDesc: 'Customize the web page title',
        menuBar: {
          title: 'Menu bar',
          mode: 'Display mode',
          modeDesc: 'Display menu bar on the screen',
          modeAuto: 'Auto hide',
          modeAlways: 'Always visible',
          keyboardLedStatus: 'Keyboard lock indicators',
          keyboardLedStatusDesc: 'Display remote Num Lock, Caps Lock, and Scroll Lock status',
          icons: 'Submenu icons',
          iconsDesc: 'Display submenu icons in the menu bar'
        }
      },
      keyboardLedStatus: {
        groupLabel: 'Remote keyboard lock status',
        indicatorLabel: '{{label}}: {{state}}',
        numLock: 'Num Lock',
        numLockShort: 'Num',
        capsLock: 'Caps Lock',
        capsLockShort: 'Caps',
        scrollLock: 'Scroll Lock',
        scrollLockShort: 'Scr',
        on: 'On',
        off: 'Off',
        unknown: 'Unknown',
        keyboardDisabled: 'USB keyboard is disabled in USB composition'
      },
      device: {
        title: 'Device',
        cpuFrequency: {
          confirmOverclock: 'Overclock the CPU to {{mhz}} MHz?',
          confirmBoot: 'Apply the CPU frequency at every start?',
          applyAtBoot: 'Apply the selected frequency at startup',
          bootWarning:
            'Dangerous with overclocking: if NanoKVM cannot run at this frequency, it may stop starting and the system may need to be reflashed. If a start freezes within the first 2 minutes, the next start runs at 1000 MHz and turns this option off.',
          bootFallback:
            'The last start with overclocking did not finish, so NanoKVM started at 1000 MHz and stopped applying the frequency at startup.',
          descriptionAtBoot:
            'Applied immediately and again at every start, including overclocking. Check SoC temperature in Dashboard.',
          eco: 'Power saving',
          stock: 'Standard',
          moderate: 'Moderate overclock',
          sampleDependent: 'Depends on the individual chip',
          warning:
            'Overclocking can cause freezes, restarts, data loss or hardware damage. Stability is not guaranteed at any overclocked frequency. Thermal protection limits it to 850 MHz at 75°C.',
          throttled: 'Temperature limit active. Your selected frequency will resume after cooling.',
          title: 'CPU frequency & overclocking',
          running: 'Running: {{mhz}} MHz',
          unavailable: 'Frequency control is unavailable on this kernel',
          description:
            'Applied immediately. Standard and power-saving settings are saved; overclocking lasts until the next device reboot. Check SoC temperature in Dashboard.',
          failed: 'Could not update CPU frequency'
        },
        oled: {
          failed: 'Could not update OLED settings',
          '-1': 'Display off',
          title: 'OLED',
          description: 'Turn off OLED screen after',
          0: 'Never',
          15: '15 sec',
          30: '30 sec',
          60: '1 min',
          180: '3 min',
          300: '5 min',
          600: '10 min',
          1800: '30 min',
          3600: '1 hour'
        },
        mouseJiggler: {
          title: 'Mouse jiggler',
          description: 'Prevent the remote host from sleeping',
          disable: 'Disable',
          absolute: 'Absolute mode',
          relative: 'Relative mode'
        },
        mdns: {
          description: 'Enable mDNS discovery service',
          tip: "Turning it off if it's not needed"
        },
        hdmi: {
          description: 'Enable HDMI/monitor output',
          idleTimeoutTitle: 'Capture idle timeout',
          idleTimeoutDescription: 'Stop HDMI capture after there are no active viewers for',
          minutes: 'min'
        },
        reboot: 'Reboot',
        rebootDesc: 'Are you sure you want to reboot NanoKVM?',
        okBtn: 'Yes',
        cancelBtn: 'No'
      },
      usb: {
        confirmDisable: 'Turn off USB?',
        confirmDisableDescription:
          'The computer loses the NanoKVM keyboard, mouse, storage and audio until USB is turned on again here. RustDesk loses keyboard and mouse control too.',
        pointerProfile: 'Absolute pointer profile',
        pointerProfileHelp:
          'Windows binds the pointer to the captured monitor (Windows 10 1903 or newer). Applying reconnects USB and HDMI.',
        pointerProfileDefault: 'Default',
        off: 'Off',
        title: 'USB composition',
        enabled: 'USB devices',
        budgetTitle: 'Endpoint budget',
        budgetExceeded: 'This selection exceeds the USB endpoint budget.',
        presetLabel: 'Composition',
        custom: 'Custom composition',
        customDescription: 'Select the USB functions you need below.',
        manual: 'Configure manually',
        apply: 'Apply',
        cancel: 'Discard changes',
        reload: 'Refresh status',
        empty: 'All USB functions will be disconnected from the connected computer.',
        loadFailed: 'Could not load the USB composition.',
        changed: 'The composition changed in another session. Current settings have been reloaded.',
        reconnectFailed: 'Connection interrupted. Reconnect to NanoKVM and refresh its status.',
        serverUpdateRequired: 'Update the NanoKVM server to apply complete compositions.',
        reconnectNotice: 'Applying briefly reconnects the USB devices.',
        presets: {
          standard: {
            title: 'KVM + USB network + disk',
            description: 'Keyboard, both mouse modes, network adapter and virtual disk.'
          },
          console: {
            title: 'KVM + serial + disk',
            description: 'Keyboard, both mouse modes, serial console and virtual disk.'
          },
          headless: {
            title: 'Headless: network + serial + disk',
            description: 'USB network, serial console and virtual disk, without keyboard or mouse.'
          },
          control: {
            title: 'Keyboard and mouse only',
            description: 'Keyboard, relative mouse and absolute pointer.'
          },
          compatibility: {
            title: 'HID compatibility (USB 1.1)',
            description: 'Keyboard and both mouse modes using the HID-only compatibility profile.'
          }
        },
        updateFailed: 'Failed to update the USB composition.',
        devices: {
          keyboard: {
            title: 'Keyboard',
            description: 'Boot-compatible keyboard and LED reports'
          },
          relative: {
            title: 'Relative mouse',
            description: 'Pointer-lock mouse for firmware, installers and games'
          },
          absolute: {
            title: 'Absolute pointer',
            description: 'Direct screen-coordinate mapping without pointer drift'
          },
          network: {
            title: 'Virtual network',
            description: 'NCM network adapter on the remote host'
          },
          disk: {
            title: 'Virtual disk',
            description: 'Present a mounted image as a USB drive'
          },
          audio: {
            title: 'USB audio',
            description: 'Stereo audio output for the connected computer (48 kHz, 16-bit)'
          },
          serial: {
            title: 'USB serial console',
            description: 'CDC ACM serial port for the connected computer'
          }
        }
      },
      network: {
        title: 'Network',
        general: 'General',
        gateway: {
          title: 'Preferred gateway',
          description:
            'When both Ethernet and Wi-Fi have an Internet gateway, choose which connection NanoKVM uses by default.',
          auto: 'Automatic',
          none: 'Connect Ethernet or Wi-Fi to choose a preferred gateway.',
          metric: 'route priority {{value}}',
          loadFailed: 'Could not read gateway routes.',
          saveFailed: 'Could not save the preferred gateway.'
        },
        wifi: {
          confirmDisable: 'Turn off Wi-Fi?',
          confirmDisableDescription:
            'Without Ethernet, NanoKVM leaves the network and this page loses its connection.',
          loading: 'Loading…',
          noAdapter: 'Wi-Fi adapter not detected',
          notDetected: 'not detected',
          disabled: 'Off',
          band: 'Frequency band',
          band24: '2.4 GHz',
          band5: '5 GHz',
          preferredBand: 'Preferred frequency band',
          preferredBandHint:
            'Used for the next Wi-Fi connection. The other band remains available as a fallback.',
          scan: 'Scan networks',
          scanFailed: 'Could not scan networks. Try again.',
          availableNetworks: 'Available networks',
          bandsUnavailable: 'Could not read the adapter frequency bands.',
          noNetworks: 'No networks found in this band.',
          manual: 'Connect manually',
          hidden: 'Hidden network',
          security: 'Security',
          open: 'Open network',
          unsupported: 'Unsupported security',
          reconnect: 'Reconnect',
          applying: 'Applying… The connection to this device may briefly be interrupted.',
          operationFailed: 'Could not apply Wi-Fi settings. Try again.',
          statusFailed: 'Could not read Wi-Fi status.',
          connectionTimeout:
            'Connection could not be confirmed. Check the network and reconnect to the device if its address changed.',
          passwordHint: '8–63 bytes (at least 8 characters for an ASCII password).',

          title: 'Wi-Fi',
          description: 'Configure Wi-Fi',
          apMode: 'AP mode is enabled, connect to Wi-Fi by scanning QR code',
          connect: 'Join Wi-Fi',
          connectDesc1: 'Please enter the network ssid and password',
          connectDesc2: 'Please enter the password to join this network',
          disconnect: 'Are you sure to disconnect the network?',
          failed: 'Connection failed, please try again.',
          ssid: 'Name',
          password: 'Password',
          joinBtn: 'Join',
          confirmBtn: 'OK',
          cancelBtn: 'Cancel'
        },
        tls: {
          confirmEnable: 'Turn on HTTPS?',
          confirmDisable: 'Turn off HTTPS?',
          confirmRestart:
            'The web server restarts and this page reloads at the new address. Other open sessions disconnect.',
          description: 'Enable HTTPS protocol',
          tip: 'Be aware: Using HTTPS can increase latency, especially with MJPEG video mode.',
          failed: 'Failed to change HTTPS. The current setting is unchanged.'
        },
        ipv6: {
          description: 'Disabled by default. Changes are saved across reboots.',
          enable: 'Enable IPv6',
          unsupported: 'IPv6 is unavailable in this kernel.',
          waiting: 'Waiting for an IPv6 address…',
          global: 'Global address',
          linkLocal: 'Local link only',
          private: 'Private address',
          disconnectHint:
            'Disabling IPv6 closes IPv6 connections. You can reconnect using the IPv4 address.',
          loadFailed: 'Failed to read IPv6 status.',
          saveFailed: 'Failed to change IPv6. If connected over IPv6, reconnect using IPv4.'
        },
        ethernet: {
          title: 'Ethernet IPv4',
          name: 'Ethernet',
          description: 'Choose DHCP or configure a persistent static IPv4 address',
          enable: 'Ethernet',
          disabled: 'Disabled',
          interfaceDown: 'Enabled in settings · interface is currently down',
          linkUp: 'Enabled · cable connected',
          noCable: 'Enabled · cable disconnected',
          disconnectWarning:
            'Saving will immediately end connections using Ethernet. Make sure another management connection is available.',
          dhcp: 'DHCP',
          static: 'Static',
          ipv4: 'IPv4 configuration',
          dhcpDescription: 'IP address and gateway are obtained automatically from DHCP',
          staticDescription: 'Settings are applied immediately and retained after a reboot',
          ipAddress: 'IP address',
          addressPlaceholder: '192.168.10.32',
          subnetMask: 'Subnet mask',
          subnetMaskPlaceholder: '255.255.255.0',
          gateway: 'Gateway',
          gatewayPlaceholder: '192.168.10.1',
          invalid: 'Enter a valid IPv4 address, subnet mask, and gateway',
          vlan: 'VLAN',
          vlanDescription: 'Use the selected DHCP or static IPv4 configuration on eth0.<ID>',
          vlanWarning:
            'Changing VLAN may disconnect this Ethernet session. Make sure the switch port allows the selected tagged VLAN.',
          vlanId: 'VLAN ID',
          invalidVlan: 'VLAN ID must be between 1 and 4094',
          saved: 'Ethernet settings saved. Network interfaces are restarting.',
          save: 'Save',
          unsaved: 'Unsaved changes',
          saveFailed: 'Failed to save Ethernet settings',
          loadFailed: 'Failed to load Ethernet settings'
        },
        dns: {
          title: 'DNS',
          description: 'Configure DNS servers for NanoKVM',
          dhcp: 'DHCP',
          manual: 'Manual',
          add: 'Add DNS',
          save: 'Save',
          invalid: 'Please enter a valid IP address',
          saved: 'DNS settings saved',
          saveFailed: 'Failed to save DNS settings',
          unsaved: 'Unsaved changes',
          maxServers: 'Maximum {{count}} DNS servers allowed',
          dnsServers: 'DNS servers',
          dhcpServersDescription: 'DNS servers are automatically obtained from DHCP',
          manualServersDescription: 'DNS servers can be edited manually',
          networkDetails: 'Network details',
          interface: 'Interface',
          ipAddress: 'IP address',
          subnetMask: 'Subnet mask',
          router: 'Router',
          none: 'None',
          server: 'DNS server {{index}}',
          remove: 'Remove DNS server {{index}}'
        }
      },
      tailscale: {
        title: 'Tailscale',
        restart: 'Restart Tailscale?',
        restartAction: 'Restart Tailscale',
        stopAction: 'Stop Tailscale',
        moreActions: 'More actions',
        stop: 'Stop Tailscale?',
        stopDesc: 'Sign out of Tailscale and disable automatic startup on boot.',
        loading: 'Loading…',
        statusFailed: 'Could not get Tailscale status',
        notInstall: 'Tailscale is not installed.',
        installDescription: 'Install Tailscale on demand. Existing settings are preserved.',
        install: 'Install Tailscale',
        installing: 'Installing Tailscale',
        failed: 'Install failed',
        retry: 'Please retry the installation.',
        notRunning: 'Tailscale is not running. Please start it to continue.',
        run: 'Start',
        notLogin:
          'The device has not been bound yet. Please sign in and bind this device to your account.',
        urlPeriod: 'This url is valid for 10 minutes',
        login: 'Sign in',
        loginSuccess: 'I have signed in',
        enable: 'Enable Tailscale',
        deviceName: 'Device name',
        deviceIP: 'Device IP',
        account: 'Account',
        logout: 'Sign out',
        logoutDesc: 'Are you sure you want to sign out?',
        uninstall: 'Uninstall Tailscale',
        uninstallDesc: 'Are you sure you want to uninstall Tailscale?',
        okBtn: 'Yes',
        cancelBtn: 'No',
        startFailed: 'Could not start Tailscale',
        loginFailed: 'Sign-in failed',
        logoutFailed: 'Sign-out failed'
      },
      netbird: {
        title: 'NetBird',
        restart: 'Restart NetBird?',
        restartAction: 'Restart NetBird',
        stopAction: 'Stop NetBird',
        moreActions: 'More actions',
        stop: 'Stop NetBird?',
        stopDesc: 'Stop the NetBird service.',
        loading: 'Loading…',
        statusUnknown: 'NetBird status is unknown',
        statusStale: 'Showing the last confirmed NetBird status',
        notInstall: 'NetBird is not installed.',
        installDescription: 'Install NetBird on demand. Existing settings are preserved.',
        install: 'Install NetBird',
        installing: 'Installing NetBird',
        notRunning: 'NetBird service is not running.',
        run: 'Start',
        notLogin:
          'The device has not been bound yet. Please sign in and bind this device to your account.',
        urlPeriod: 'This url is valid for 10 minutes',
        login: 'Sign in',
        loginSuccess: 'I have signed in',
        enable: 'Enable NetBird',
        deviceName: 'Device name',
        deviceIP: 'Device IP',
        uninstall: 'Uninstall NetBird',
        uninstallDesc: 'Are you sure you want to uninstall NetBird?',
        uninstallWarning:
          'If you are connected through NetBird, this ends that connection. Existing device settings remain.',
        version: 'Version',
        disconnect: 'Disconnect',
        disconnectConfirm: 'Are you sure you want to disconnect?',
        okBtn: 'Yes',
        cancelBtn: 'No',
        error: {
          title: 'NetBird operation failed',
          intro: 'Error details:',
          stepWait: '1. Wait 10-15 seconds and retry the action.',
          stepRestartUI: '2. Click "Restart service" below.',
          stepRestartSSH: '3. If needed, run: /etc/init.d/S99netbird restart',
          stepReboot: '4. Reboot NanoKVM only if the steps above do not help.',
          restartButton: 'Restart service',
          refreshButton: 'Refresh status',
          restartFailed: 'Restart failed',
          stopFailed: 'Stop failed',
          statusFailed: 'Could not get NetBird status',
          requestFailed: 'Request failed',
          installFailed: 'Install failed',
          startFailed: 'Start failed',
          loginFailed: 'Sign-in failed',
          disconnectFailed: 'Disconnect failed',
          uninstallFailed: 'Uninstall failed'
        }
      },
      rustdesk: {
        absent: 'Not installed',
        running: 'Running',
        stopped: 'Stopped',
        registered: 'Registered',
        registering: 'Waiting for ID server',
        install: 'Install',
        remove: 'Remove',
        upgrade: 'Update to {{version}}',
        addonVersion: 'Add-on version',
        rustdeskVersion: 'RustDesk protocol base',
        unknownVersion: 'Unknown',
        save: 'Save settings',
        enabled: 'Enable remote access',
        server: 'Server',
        public: 'Official public servers',
        custom: 'Custom server',
        idServer: 'ID / rendezvous server',
        relay: 'Relay server (optional)',
        key: 'Server public key (optional)',
        password: 'Access password',
        passwordMode: 'Password',
        temporary: 'Temporary (automatic)',
        permanent: 'Permanent',
        temporaryPassword: 'Temporary password',
        newPassword: 'New password',
        temporaryHint:
          'Share this ID and password. The password changes when RustDesk starts or when you request a new one.',
        rotatingTemporaryHint:
          'Share this ID and the current password. Each new successful sign-in generates a new password without closing active sessions. RustDesk also replaces it on startup or when you request a new one.',
        startForPassword: 'Enable RustDesk and save settings to generate a password.',
        waitingForPassword: 'Generating a temporary password…',
        regenerateConfirm: 'Generate a new password? Current RustDesk connections will close.',
        advanced: 'Advanced settings',
        audioEnabled:
          'USB audio is enabled. Select NanoKVM as the audio output on the connected computer. Sound can be muted in the RustDesk client.',
        audioDisabled:
          'To stream computer sound, enable USB audio in the device USB settings and select NanoKVM as the audio output on the computer.',
        webrtc: 'Allow WebRTC connections',
        transportHint:
          'TCP and relay are used by default. WebRTC may reduce frame rate on NanoKVM. To connect by TCP using an ID, turn WebRTC off in the RustDesk client too.',
        open: 'Open management',
        settings: 'RustDesk',
        keepPassword: 'Leave empty to keep the current password',
        inputDefaults:
          'Enabling remote access automatically enables USB keyboard and absolute/relative mouse.',
        transmitAudio: 'Transmit sound',
        audioHint:
          'Enables USB audio automatically. Select NanoKVM as the sound output on the connected computer.',
        audioMuted: 'Sound transmission through RustDesk is disabled.',
        clients: 'Maximum viewers',
        unavailable:
          'RustDesk is not available in the configured APK repositories. Add the repository containing nanokvm-rustdesk, or install its APK through Packages.',
        explain:
          'Connect a RustDesk client to this ID to view HDMI and control USB keyboard and mouse.',
        video:
          'Video automatically uses the codec selected in NanoKVM video settings. H.265 requires client support. Reconnect after changing the device codec. Browser takeover releases RustDesk input.',
        deletion: 'Remove the package? Server settings, password and device ID are preserved.',
        failed: 'The request failed',
        saved: 'Settings saved',
        passwordUpdated: 'Password updated',
        description: 'Remote access to the computer connected to NanoKVM, using RustDesk.',
        done: 'Package operation completed',
        id: 'RustDesk ID',
        source: 'Source and license',
        passwordRequired: 'Enter a password of 8 to 64 bytes',
        sameServer: 'Configure the same custom ID server and public key in the RustDesk client.'
      },
      account: {
        title: 'Users',
        webAccount: 'Web account name',
        role: 'Role',
        roles: {
          admin: 'Administrator',
          user: 'User'
        },
        password: 'Password',
        updateBtn: 'Change',
        logoutBtn: 'Sign out',
        logoutDesc: 'Are you sure you want to sign out?',
        logoutFailed: 'Sign-out failed; this session is still signed in.',
        okBtn: 'Yes',
        cancelBtn: 'No',
        users: {
          title: 'Users',
          create: 'Create user',
          enabled: 'Enabled',
          enableUser: 'Enable {{username}}',
          disabled: 'Disabled',
          deviceOwner: 'Device owner',
          rename: 'Rename',
          resetPassword: 'Reset password',
          delete: 'Delete',
          deleteConfirm: 'Delete this user and revoke all of their sessions?',
          created: 'User created',
          deleted: 'User deleted',
          passwordUpdated: 'Password updated',
          usernameUpdated: 'Username updated',
          loadFailed: 'Failed to load users',
          saveFailed: 'Failed to save user',
          deleteFailed: 'Failed to delete user'
        }
      },
      extensions: {
        title: 'Extensions'
      }
    },
    picoclaw: {
      moreActions: 'More actions',
      title: 'PicoClaw assistant',
      empty: 'Open the panel and start a task to begin.',
      inputPlaceholder: 'Describe what you want the PicoClaw to do',
      newConversation: 'New conversation',
      processing: 'Processing…',
      agent: {
        defaultTitle: 'General assistant',
        defaultDescription: 'General chat, search, and workspace help.',
        kvmTitle: 'Remote control',
        kvmDescription: 'Operate the remote host through NanoKVM.',
        switched: 'Agent role switched',
        switchFailed: 'Failed to switch agent role',
        label: 'Agent role'
      },
      send: 'Send',
      cancel: 'Cancel',
      status: {
        connecting: 'Connecting to gateway…',
        connected: 'PicoClaw session connected',
        disconnected: 'PicoClaw session closed',
        stopped: 'Stop request sent',
        runtimeStarted: 'PicoClaw runtime started',
        runtimeStartFailed: 'Failed to start PicoClaw runtime',
        runtimeStopped: 'PicoClaw runtime stopped',
        runtimeStopFailed: 'Failed to stop PicoClaw runtime',
        controlSwitchedToMCP: 'Control switched to the external MCP service'
      },
      connection: {
        runtime: {
          checking: 'Checking',
          restoring: 'Restoring PicoClaw',
          ready: 'Runtime ready',
          stopped: 'Runtime stopped',
          blockedByMCP: 'External MCP control is active',
          readyBlockedByMCP:
            'The runtime is running, but external MCP currently controls device input.',
          readyWithoutControl:
            'The runtime is running. Grant PicoClaw device control before reconnecting.',
          unavailable: 'Runtime unavailable',
          configError: 'Configuration error'
        },
        transport: {
          connecting: 'Connecting',
          connected: 'Connected',
          disconnected: 'Disconnected',
          reconnect: 'Reconnect',
          reconnectDescription: 'Reconnect to the running PicoClaw session.',
          reconnectBlocked: 'PicoClaw needs device control before reconnecting.'
        },
        run: {
          idle: 'Idle',
          busy: 'Busy'
        }
      },
      message: {
        toolAction: 'Action',
        observation: 'Observation',
        screenshot: 'Screenshot'
      },
      overlay: {
        locked: 'PicoClaw is controlling the device. Manual input is paused.'
      },
      control: {
        picoclaw: 'Device control: PicoClaw',
        picoclawDescription: 'PicoClaw can write keyboard and mouse input. Manual input may pause.',
        mcp: 'Device control: external MCP',
        mcpDescription: 'External MCP can write to the device. PicoClaw will not take over input.',
        off: 'Device control: manual/no AI',
        offDescription:
          'AI will not write keyboard or mouse input. Manual control remains available.',
        transitioning: 'Device control: switching',
        transitioningDescription: 'Device control is syncing. Please wait.',
        grant: 'Take over',
        release: 'Return control',
        releasing: 'Releasing…',
        switching: 'Switching…',
        releasingLabel: 'Device control: releasing',
        releasingDescription:
          'Device control is being returned. PicoClaw has stopped current writes.',
        granted: 'PicoClaw control granted',
        released: 'Device control returned',
        grantFailed: 'Failed to grant PicoClaw control',
        releaseFailed: 'Failed to release PicoClaw control',
        grantConfirmTitle: 'Switch device control to PicoClaw?',
        grantConfirmDesc: 'External MCP device writes will be interrupted.'
      },
      install: {
        install: 'Install PicoClaw',
        installing: 'Installing PicoClaw',
        success: 'PicoClaw installed successfully',
        failed: 'Failed to install PicoClaw',
        uninstalling: 'Uninstalling runtime…',
        uninstalled: 'Runtime uninstalled successfully.',
        uninstallFailed: 'Uninstall failed.',
        requiredTitle: 'PicoClaw is not installed',
        requiredDescription: 'Install PicoClaw before starting the PicoClaw runtime.',
        progressDescription: 'PicoClaw is being downloaded and installed.',
        stages: {
          preparing: 'Preparing',
          downloading: 'Downloading',
          extracting: 'Extracting',
          verifying: 'Verifying',
          installing: 'Installing',
          installed: 'Installed',
          install_timeout: 'Timed out',
          install_failed: 'Failed'
        }
      },
      model: {
        requiredTitle: 'Model configuration is required',
        requiredDescription: 'Configure the PicoClaw model before using PicoClaw chat.',
        docsTitle: 'Configuration guide',
        docsDesc: 'Supported models and protocols',
        menuLabel: 'Configure model',
        modelIdentifier: 'Model identifier',
        modelIdentifierPlaceholder: 'openai/gpt-5.4',
        apiBase: 'API base URL',
        apiBasePlaceholder: 'https://api.example.com/v1',
        apiKey: 'API key',
        apiKeyPlaceholder: 'Enter the model API key',
        apiKeyOptionalPlaceholder: 'Optional for this local provider',
        save: 'Save',
        saving: 'Saving',
        saved: 'Model configuration saved',
        saveFailed: 'Failed to save model configuration',
        invalidNoKey: 'Model identifier and API base URL are required',
        invalid: 'Model identifier, API base URL, and API key are required'
      },
      uninstall: {
        menuLabel: 'Uninstall',
        confirmTitle: 'Uninstall PicoClaw',
        confirmContent:
          'Are you sure you want to uninstall PicoClaw? This will delete the executable and all configuration files.',
        confirmOk: 'Uninstall',
        confirmCancel: 'Cancel'
      },
      history: {
        title: 'History',
        loading: 'Loading sessions…',
        emptyTitle: 'No history yet',
        emptyDescription: 'Previous PicoClaw sessions will appear here.',
        loadFailed: 'Failed to load session history',
        deleteFailed: 'Failed to delete session',
        deleteConfirmTitle: 'Delete session',
        deleteConfirmContent: 'Are you sure you want to delete "{{title}}"?',
        deleteConfirmOk: 'Delete',
        deleteConfirmCancel: 'Cancel',
        messageCount_one: '{{count}} message',
        messageCount_other: '{{count}} messages',
        messageCount: '{{count}} messages'
      },
      config: {
        startRuntime: 'Start PicoClaw',
        stopRuntime: 'Stop PicoClaw'
      },
      start: {
        enableConfirmTitle: 'Switch control to PicoClaw?',
        enableConfirmDesc: 'External MCP device writes will be interrupted before PicoClaw starts.',
        enableConfirmOk: 'Start PicoClaw',
        enableConfirmCancel: 'Cancel',
        title: 'Start PicoClaw',
        description: 'Start the runtime to begin using the PicoClaw assistant.',
        switchFromMCP: 'Switch to PicoClaw and start',
        takeoverAndStart: 'Take over and start'
      }
    },
    error: {
      title: "We've ran into an issue",
      refresh: 'Refresh',
      requestFailed: 'The request failed. Check the connection and try again.'
    },
    fullscreen: {
      toggle: 'Toggle fullscreen'
    },
    menu: {
      collapse: 'Collapse menu',
      expand: 'Expand menu'
    }
  }
};

export default en;
