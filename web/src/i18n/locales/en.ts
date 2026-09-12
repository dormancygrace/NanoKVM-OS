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

    dateTime: {
      title: 'Date & Time',
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
      openvpnNote:
        'One OpenVPN profile can be enabled at a time. Enabled profiles reconnect after a restart. Routed TUN profiles are supported; TAP, scripts and interactive SSO/MFA are not. Server DNS applies to the whole device while connected.',
      credentials: 'Credentials',
      credentialsRequired: 'Credentials required',
      username: 'Username',
      password: 'Password',
      passphrase: 'Private-key passphrase',
      save: 'Save',

      keyboardDisabled: 'USB keyboard is disabled in USB Composition',
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
        'Only the interface subnet is routed by default. Enable Route Allowed IPs to add the peer routes.',
      routeAllowedIPs: 'Route Allowed IPs',
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
      apply: 'Apply',
      discard: 'Discard changes',
      pending: 'Changes have not been applied',
      applied: 'Video settings applied',
      unstableTitle: 'QHD H.265 WebRTC is unstable',
      unstableDescription: 'This mode can freeze or restart the device. Use H.265 Direct for QHD.',
      unstableTag: 'QHD unstable',
      title: 'Video',
      open: 'Video settings…',
      description: 'Configure the HDMI monitor independently from the video sent to your browser.',
      sameAsInput: 'Same as input',
      atMost: 'Up to {{value}}',
      streamResolution: 'Stream resolution',
      limitHint:
        'Keep the source aspect ratio. Larger input is reduced; smaller input is never enlarged.',
      failed: 'Could not apply video settings.',
      bitrate: 'Bitrate',
      current: 'Current video',
      captureOn: 'Capture enabled',
      captureOff: 'Capture disabled',
      input: 'HDMI input',
      output: 'Encoded stream',
      requested: 'Requested frame rate',
      measured: 'Server output rate',
      monitor: 'HDMI monitor',
      monitorProfile: 'Virtual monitor profile',
      automatic: 'Automatic (recommended)',
      preferFhd: 'Prefer 1920 × 1080 · 60 Hz',
      preferQhd: 'Prefer 2560 × 1440 · 30 Hz',
      monitorHint:
        'Advertises a preferred mode and fallback timings. BIOS and the operating system may choose different resolutions; capture follows the actual signal automatically. Changing this profile briefly reconnects HDMI.',
      monitorUnavailable:
        'Changing the virtual monitor profile is unavailable on this device. Input detection remains automatic.',
      stream: 'Video stream',
      streamHint:
        'Resolution, FPS and bitrate affect all viewers. Changing stream resolution does not change the computer’s desktop. New viewers automatically use the active encoder codec; transport and display scale remain individual.',
      advanced: 'Advanced and recovery',
      gopHint:
        'GOP is the interval between keyframes. HDMI recovery restarts capture if the source stops responding.',
      fpsLimited:
        'Current QHD input limits capture to {{fps}} FPS. The saved FPS request is kept for the next source mode.',
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
      desktop: 'Remote Desktop',
      login: 'Login',
      changePassword: 'Change Password',
      terminal: 'Terminal',
      wifi: 'Wi-Fi'
    },
    auth: {
      login: 'Login',
      placeholderUsername: 'Username',
      placeholderPassword: 'Password',
      placeholderCurrentPassword: 'Current password',
      placeholderPassword2: 'Please enter password again',
      noEmptyUsername: 'Username required',
      noEmptyPassword: 'Password required',
      passwordLength: 'Password must be between 8 and 72 characters',
      noAccount: 'Failed to get user information, please refresh web page or reset password',
      invalidUser: 'Invalid username or password',
      locked: 'Too many logins, please try again later',
      globalLocked: 'System under protection, please try again later',
      error: 'Unexpected error',
      invalidCurrentPassword: 'Current password is incorrect',
      changePassword: 'Change Password',
      changePasswordDesc: 'For the security of your device, please change the password!',
      differentPassword: 'Passwords do not match',
      illegalUsername: 'Username contains illegal characters',
      illegalPassword: 'Password contains illegal characters',
      forgetPassword: 'Forgot Password',
      ok: 'Ok',
      cancel: 'Cancel',
      loginButtonText: 'Login',
      tips: {
        reset1:
          'To reset the passwords, press and hold the BOOT button on the NanoKVM for 10 seconds.',
        reset2: 'For detailed steps, please consult this document:',
        reset3: 'Web default account:',
        reset4: 'SSH default account:',
        change1: 'Please note that this action will change the following passwords:',
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
      invalidMode:
        'The current mode does not support network setup. Please go to your device and enable Wi-Fi configuration mode.',
      confirmBtn: 'Ok',
      finishBtn: 'Finished',
      ap: {
        authTitle: 'Authentication Required',
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
      video: 'Video Mode',
      codec: 'Codec',
      unsupported: 'unsupported',
      videoDirectTips: 'Enable HTTPS in "Settings > Device" to use this mode',
      resolution: 'Resolution',
      controlRegion: {
        title: 'Mouse Calibration',
        description:
          'Use this setting when the controlled device uses a non-16:9 resolution and the cursor is misaligned horizontally or vertically.',
        off: 'Off',
        auto: 'Auto',
        autoWarning: 'Calibration may fail when the user application has a pure black background.',
        manual: 'Manual',
        selectedResolution: 'Selected Area Resolution',
        unused: 'Not used',
        originalResolution: 'Original Resolution',
        selectResolution: 'Select original resolution',
        addResolution: 'Add custom resolution',
        add: 'Add',
        duplicateResolution: 'This resolution already exists.',
        width: 'Width',
        height: 'Height',
        apply: 'Calculate and Apply',
        invalidResolution: 'Enter a valid original resolution after the video is ready.',
        select: 'Select Area',
        clear: 'Restore Automatic',
        saveFailed: 'Failed to save the input area.',
        tooSmall: 'The selected area is too small.',
        previewUnavailable: 'Preview unavailable',
        clearConfirm: 'Restore automatic black-border detection?',
        dragHint: 'Drag to select the remote desktop area',
        finish: 'Done',
        confirm: 'Confirm',
        cancel: 'Cancel'
      },
      auto: 'Automatic',
      autoTips:
        'Restores the default monitor profile and lets the connected computer choose its resolution.',
      fps: 'FPS',
      customizeFps: 'Customize',
      quality: 'Quality',
      qualityLossless: 'Lossless',
      qualityHigh: 'High',
      qualityMedium: 'Medium',
      qualityLow: 'Low',
      frameDetect: 'Frame Detect',
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
        retrieving: 'Getting screen...',
        changingResolution: 'Switching resolution...',
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
      readClipboard: 'Read from Clipboard',
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
        title: 'Leader Key',
        desc: 'Bypass browser restrictions and send system shortcuts directly to the remote host.',
        howToUse: 'How to Use',
        simultaneous: {
          title: 'Simultaneous Mode',
          desc1: 'Press and hold the Leader Key, then press the shortcut.',
          desc2: 'Intuitive, but may conflict with system shortcuts.'
        },
        sequential: {
          title: 'Sequential Mode',
          desc1:
            'Press the Leader Key → press the shortcut in sequence → press the Leader Key again.',
          desc2: 'Requires more steps, but completely avoids system conflicts.'
        },
        enable: 'Enable Leader Key',
        tip: 'When assigned as a Leader Key, this key functions exclusively as a shortcut trigger and loses its default behavior.',
        placeholder: 'Please press the Leader Key',
        shiftRight: 'Right Shift',
        ctrlRight: 'Right Ctrl',
        metaRight: 'Right Win',
        submit: 'Submit',
        recorder: {
          rec: 'REC',
          activate: 'Activate keys',
          input: 'Please press the shortcut...'
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
        title: 'Input Adapter',
        auto: 'Auto',
        'pointer-lock': 'Pointer Lock',
        touchpad: 'Touchpad'
      },
      touchpadGuide: {
        title: 'Touchpad guide',
        scope: 'Applies when Input Adapter is Touchpad and Mouse Mode is Relative.',
        swipeTitle: 'Swipe to move',
        swipeDesc: 'Swipe inside the active screen area to move the remote pointer.',
        tapTitle: 'Tap to click',
        tapDesc: 'A short tap sends one left click.',
        holdTitle: 'Hold for left button down',
        holdDesc: 'Touch and keep still for about 1 second to hold the left mouse button down.',
        dragTitle: 'Move after hold to drag',
        dragDesc: 'After the hold is active, move your finger to drag with the left button held.'
      },
      resetHid: 'Reset HID',
      hidOnly: {
        title: 'HID-Only mode',
        desc: "If your mouse and keyboard stop responding and resetting HID doesn't help, it could be a compatibility issue between the NanoKVM and the device. Try to enable HID-Only mode for better compatibility.",
        tip1: 'Enabling HID-Only mode will unmount the virtual U-disk and virtual network',
        tip2: 'In HID-Only mode, image mounting is disabled',
        tip3: 'NanoKVM will automatically reboot after switching modes',
        enable: 'Enable HID-Only mode',
        disable: 'Disable HID-Only mode'
      }
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
      loading: 'Loading...',
      empty: 'Nothing Found',
      mountMode: 'Mount mode',
      mountFailed: 'Mount failed',
      mountDesc:
        'On some systems, you need to eject the virtual disk from the remote host before mounting the image.',
      unmountFailed: 'Unmount failed',
      unmountDesc:
        'On some systems, you need to manually eject from the remote host before unmounting the image.',
      refresh: 'Refresh the image list',
      attention: 'Attention',
      deleteConfirm: 'Are you sure you want to delete this image?',
      okBtn: 'Yes',
      cancelBtn: 'No',
      tips: {
        title: 'How to upload',
        usb1: 'Connect the NanoKVM to your computer via USB.',
        usb2: 'Ensure that the virtual disk is mounted (Settings - Virtual Disk).',
        usb3: 'Open the virtual disk on your computer and copy the image file to the root directory of the virtual disk.',
        scp1: 'Make sure the NanoKVM and your computer are on the same local network.',
        scp2: 'Open a terminal on your computer and use the SCP command to upload the image file to the /data directory on the NanoKVM.',
        scp3: 'Example: scp your-image-path root@your-nanokvm-ip:/data',
        tfCard: 'TF Card',
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
      runBackground: 'Run Background',
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
      nanokvm: 'NanoKVM Terminal',
      usbSerial: 'USB serial console',
      serial: 'Serial Port Terminal',
      serialPort: 'Serial Port',
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
      confirm: 'Ok'
    },
    wol: {
      title: 'Wake-on-LAN',
      sending: 'Sending command...',
      sent: 'Command sent',
      input: 'Please enter the MAC',
      ok: 'Ok'
    },
    download: {
      title: 'Image Downloader',
      input: 'Please enter a remote image URL',
      ok: 'Ok',
      disabled: '/data partition is RO, so we cannot download the image',
      uploadbox: 'Drop file here or click to select',
      inputfile: 'Please enter the image File',
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
      resetConfirm: 'Proceed reset operation?',
      powerConfirm: 'Proceed power operation?',
      okBtn: 'Yes',
      cancelBtn: 'No'
    },
    settings: {
      updates: {
        title: 'Updates',
        description:
          'Update NanoKVM OS using signed packages. System updates restart the device; application updates briefly disconnect video and control.',
        installed: 'Installed',
        latest: 'Latest release',
        automatic:
          'The device checks this GitHub repository automatically once a day. Installation always requires your action.',
        check: 'Check for updates',
        download: 'Download and verify',
        file: 'Signed update package (.nkos)',
        uploading: 'Uploading…',
        verifying: 'Verifying package…',
        verify: 'Upload and verify',
        install: 'Install',
        installRestart: 'Install and restart',
        compatibility:
          'Compatible signed NanoKVM OS packages can update the application and system components. Settings are preserved. Kernel packages replace the kernel and matching modules after a restart. There is no automatic kernel rollback; failed boot requires reflashing the SD card. Original NanoKVM archives are rejected.',
        requestFailed: 'The update request failed. Check device connectivity and retry.',
        tooLarge: 'The package exceeds 192 MiB.',
        reconnecting: 'Waiting for the device to reconnect…',
        reload: 'Reload interface'
      },
      memory: {
        recompressTitle: 'Recompress cold pages with ZSTD',
        recompressDescription:
          'Keep fast LZ4 for new pages. Every 15 seconds, try up to 1 MiB of pages untouched for a minute. Uses extra CPU and compressor memory; reading these pages may be slower. Changing this mode recreates ZRAM and requires enough free RAM.',
        recompressNotReady:
          'The saved mode is not active. Reapply it when sufficient RAM is available.',
        applicationTitle: 'Reduce application memory',
        applicationTip:
          'Use a {{limit}} MiB soft limit for Go-managed memory. Applies to NanoKVM immediately and to Tailscale when it next starts. Garbage collection may use more CPU. This does not cap total process RAM or video buffers.',
        applicationError: 'Could not change the application memory limit',
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
      title: 'Settings',
      back: 'Back',
      close: 'Close',
      mcp: {
        title: 'MCP Service',
        service: 'Remote control MCP',
        serviceDesc:
          'Allow trusted MCP clients to control the keyboard and mouse and capture screenshots',
        securityWarning:
          'Anyone with this API key can control the remote host and view its screen. Use HTTPS and enable it only on trusted networks.',
        endpoint: 'Endpoint',
        apiKey: 'API Key',
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
        documentation: 'Documentation',
        reportIssue: 'Report an issue',
        upstreamCredit:
          'Built on the original Sipeed NanoKVM project. Thank you to its authors and contributors.',
        systemInformation: 'System information',
        information: 'Information',
        ip: 'IP',
        mdns: 'mDNS',
        application: 'Application Version',
        applicationTip: 'NanoKVM web application version',
        image: 'Image Version',
        imageTip: 'NanoKVM system image version',
        deviceKey: 'Device Key',
        community: 'Community',
        hostname: 'Hostname',
        hostnameUpdated: 'Hostname updated. Reboot to apply.',
        ipType: {
          Wired: 'Wired',
          Wireless: 'Wireless',
          Other: 'Other'
        }
      },
      appearance: {
        title: 'Appearance',
        display: 'Display',
        language: 'Language',
        languageDesc: 'Select the language for the interface',
        webTitle: 'Web Title',
        webTitleDesc: 'Customize the web page title',
        menuBar: {
          title: 'Menu Bar',
          mode: 'Display Mode',
          modeDesc: 'Display menu bar on the screen',
          modeOff: 'Off',
          modeAuto: 'Auto hide',
          modeAlways: 'Always visible',
          keyboardLedStatus: 'Keyboard lock indicators',
          keyboardLedStatusDesc: 'Display remote Num Lock, Caps Lock, and Scroll Lock status',
          icons: 'Submenu Icons',
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
        unknown: 'Unknown'
      },
      device: {
        title: 'Device',
        cpuFrequency: {
          eco: 'Power saving',
          stock: 'Standard',
          moderate: 'Moderate overclock',
          sampleDependent: 'Depends on the individual chip',
          warning:
            'Overclocking can cause freezes, restarts, data loss or hardware damage. Stability is not guaranteed at any overclocked frequency. All overclocking is applied at runtime only. After a reboot, the CPU returns to 1000 MHz. Thermal protection limits it to 850 MHz at 75°C.',
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
        ssh: {
          description: 'Enable SSH remote access',
          tip: 'Set a strong password before enabling (Account - Change Password)'
        },
        advanced: 'Advanced Settings',
        swap: {
          title: 'Swap',
          disable: 'Disable',
          description: 'Set the swap file size',
          tip: "Enabling this feature could shorten your SD card's usable life!"
        },
        mouseJiggler: {
          title: 'Mouse Jiggler',
          description: 'Prevent the remote host from sleeping',
          disable: 'Disable',
          absolute: 'Absolute Mode',
          relative: 'Relative Mode'
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
        autostart: {
          title: 'Autostart Scripts Settings',
          description: 'Manage scripts that run automatically on system startup',
          new: 'New',
          deleteConfirm: 'Are you sure you want to delete this file?',
          yes: 'Yes',
          no: 'No',
          scriptName: 'Autostart Script Name',
          scriptContent: 'Autostart Script Content',
          settings: 'Settings'
        },
        hidOnly: 'HID-Only Mode',
        hidOnlyDesc: 'Stop emulating virtual devices, retaining only basic HID control',
        disk: 'Virtual Disk',
        diskDesc: 'Mount SD card on the remote host',
        network: 'Virtual Network',
        networkDesc: 'Mount virtual network card on the remote host',
        reboot: 'Reboot',
        rebootDesc: 'Are you sure you want to reboot NanoKVM?',
        okBtn: 'Yes',
        cancelBtn: 'No'
      },
      usb: {
        title: 'USB Composition',
        budgetTitle: 'Endpoint budget',
        budgetDescription:
          'The controller has six configured IN FIFOs and seven OUT endpoint numbers.',
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
            title: 'Relative Mouse',
            description: 'Pointer-lock mouse for firmware, installers and games'
          },
          absolute: {
            title: 'Absolute Pointer',
            description: 'Direct screen-coordinate mapping without pointer drift'
          },
          network: {
            title: 'Virtual Network',
            description: 'NCM network adapter on the remote host'
          },
          disk: {
            title: 'Virtual Disk',
            description: 'Present a mounted image as a USB drive'
          },
          audio: {
            title: 'USB Audio',
            description: 'Stereo audio output for the connected computer (48 kHz, 16-bit)'
          },
          serial: {
            title: 'USB Serial Console',
            description: 'CDC ACM serial port for the connected computer'
          }
        }
      },
      network: {
        title: 'Network',
        wifi: {
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
          confirmBtn: 'Ok',
          cancelBtn: 'Cancel'
        },
        tls: {
          description: 'Enable HTTPS protocol',
          tip: 'Be aware: Using HTTPS can increase latency, especially with MJPEG video mode.'
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
          description: 'Choose DHCP or configure a persistent static IPv4 address',
          dhcp: 'DHCP',
          static: 'Static',
          ipv4: 'IPv4 Configuration',
          dhcpDescription: 'IP address and gateway are obtained automatically from DHCP',
          staticDescription: 'Settings are applied immediately and retained after a reboot',
          ipAddress: 'IP Address',
          addressPlaceholder: '192.168.10.32',
          subnetMask: 'Subnet Mask',
          subnetMaskPlaceholder: '255.255.255.0',
          gateway: 'Gateway',
          gatewayPlaceholder: '192.168.10.1',
          invalid: 'Enter a valid IPv4 address, subnet mask, and gateway',
          save: 'Save',
          unsaved: 'Unsaved changes',
          savedStatic: 'Static address saved. Reconnect at {{address}}.',
          savedDhcp: 'DHCP enabled. Reconnect using the address assigned by your router.',
          saveFailed: 'Failed to save Ethernet settings',
          loadFailed: 'Failed to load Ethernet settings'
        },
        dns: {
          title: 'DNS',
          description: 'Configure DNS servers for NanoKVM',
          mode: 'Mode',
          dhcp: 'DHCP',
          manual: 'Manual',
          add: 'Add DNS',
          save: 'Save',
          invalid: 'Please enter a valid IP address',
          noDhcp: 'No DHCP DNS is currently available',
          saved: 'DNS settings saved',
          saveFailed: 'Failed to save DNS settings',
          unsaved: 'Unsaved changes',
          maxServers: 'Maximum {{count}} DNS servers allowed',
          dnsServers: 'DNS Servers',
          dhcpServersDescription: 'DNS servers are automatically obtained from DHCP',
          manualServersDescription: 'DNS servers can be edited manually',
          networkDetails: 'Network Details',
          interface: 'Interface',
          ipAddress: 'IP Address',
          subnetMask: 'Subnet Mask',
          router: 'Router',
          none: 'None'
        }
      },
      tailscale: {
        title: 'Tailscale',
        memory: {
          title: 'Memory optimization',
          tip: 'When memory usage exceeds the limit, garbage collection is performed more aggressively to attempt to free up memory. A Tailscale restart is required for the change to take effect.'
        },
        swap: {
          title: 'Swap memory',
          tip: 'If issues persist after enabling memory optimization, try enabling swap memory. This sets the swap file size to 256MB by default, which can be adjusted in "Settings > Device".'
        },
        restart: 'Restart Tailscale?',
        stop: 'Stop Tailscale?',
        stopDesc: 'Log out Tailscale and disable automatic startup on boot.',
        loading: 'Loading...',
        notInstall: 'Tailscale not found! Please install.',
        install: 'Install',
        installing: 'Installing',
        failed: 'Install failed',
        retry: 'Please refresh and try again. Or try to install manually',
        download: 'Download the',
        package: 'installation package',
        unzip: 'and unzip it',
        upTailscale: 'Upload tailscale to NanoKVM directory /usr/bin/',
        upTailscaled: 'Upload tailscaled to NanoKVM directory /usr/sbin/',
        refresh: 'Refresh current page',
        notRunning: 'Tailscale is not running. Please start it to continue.',
        run: 'Start',
        notLogin:
          'The device has not been bound yet. Please login and bind this device to your account.',
        urlPeriod: 'This url is valid for 10 minutes',
        login: 'Login',
        loginSuccess: 'Login Success',
        enable: 'Enable Tailscale',
        deviceName: 'Device Name',
        deviceIP: 'Device IP',
        account: 'Account',
        logout: 'Logout',
        logoutDesc: 'Are you sure you want to logout?',
        uninstall: 'Uninstall Tailscale',
        uninstallDesc: 'Are you sure you want to uninstall Tailscale?',
        okBtn: 'Yes',
        cancelBtn: 'No'
      },
      account: {
        title: 'Account',
        webAccount: 'Web Account Name',
        role: 'Role',
        roles: {
          admin: 'Administrator',
          user: 'User'
        },
        password: 'Password',
        updateBtn: 'Change',
        logoutBtn: 'Logout',
        logoutDesc: 'Are you sure you want to logout?',
        okBtn: 'Yes',
        cancelBtn: 'No',
        users: {
          title: 'Users',
          create: 'Create User',
          enabled: 'Enabled',
          disabled: 'Disabled',
          deviceOwner: 'Device owner',
          rename: 'Rename',
          resetPassword: 'Reset Password',
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
      }
    },
    picoclaw: {
      title: 'PicoClaw Assistant',
      empty: 'Open the panel and start a task to begin.',
      inputPlaceholder: 'Describe what you want the PicoClaw to do',
      newConversation: 'New conversation',
      processing: 'Processing...',
      agent: {
        defaultTitle: 'General Assistant',
        defaultDescription: 'General chat, search, and workspace help.',
        kvmTitle: 'Remote Control',
        kvmDescription: 'Operate the remote host through NanoKVM.',
        switched: 'Agent role switched',
        switchFailed: 'Failed to switch agent role'
      },
      send: 'Send',
      cancel: 'Cancel',
      status: {
        connecting: 'Connecting to gateway...',
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
        releasing: 'Releasing...',
        switching: 'Switching...',
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
        uninstalling: 'Uninstalling runtime...',
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
          install_timeout: 'Timed Out',
          install_failed: 'Failed'
        }
      },
      model: {
        requiredTitle: 'Model configuration is required',
        requiredDescription: 'Configure the PicoClaw model before using PicoClaw chat.',
        docsTitle: 'Configuration Guide',
        docsDesc: 'Supported models and protocols',
        menuLabel: 'Configure model',
        modelIdentifier: 'Model Identifier',
        modelIdentifierPlaceholder: 'openai/gpt-5.4',
        apiBase: 'API Base URL',
        apiBasePlaceholder: 'https://api.example.com/v1',
        apiKey: 'API Key',
        apiKeyPlaceholder: 'Enter the model API key',
        save: 'Save',
        saving: 'Saving',
        saved: 'Model configuration saved',
        saveFailed: 'Failed to save model configuration',
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
        loading: 'Loading sessions...',
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
      refresh: 'Refresh'
    },
    fullscreen: {
      toggle: 'Toggle Fullscreen'
    },
    menu: {
      collapse: 'Collapse Menu',
      expand: 'Expand Menu'
    }
  }
};

export default en;
