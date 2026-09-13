param(
 [Parameter(Mandatory)][string]$OutputDirectory,
 [ValidateRange(5,3600)][int]$Seconds=300,
 [string]$DeviceHost='192.168.4.128', [string]$Port='COM3',
 [ValidatePattern('^[a-zA-Z0-9-]{1,48}$')][string]$HeartbeatToken='diagnostic',
 [switch]$AllowSysRq, [switch]$AutoDumpOnLoss
)
$ErrorActionPreference='Stop'
if($AutoDumpOnLoss -and -not $AllowSysRq){throw 'Automatic dumps require explicitly enabled diagnostic SysRq'}
if(Test-Path -LiteralPath $OutputDirectory){throw 'Choose a new output directory'}
New-Item -ItemType Directory -Path $OutputDirectory | Out-Null
$OutputDirectory=(Resolve-Path -LiteralPath $OutputDirectory).Path
$clock=[Diagnostics.Stopwatch]::StartNew()
$uart=[IO.StreamWriter]::new((Join-Path $OutputDirectory 'uart-private.jsonl'),$false,[Text.UTF8Encoding]::new($false));$uart.AutoFlush=$true
$events=[IO.StreamWriter]::new((Join-Path $OutputDirectory 'events.jsonl'),$false,[Text.UTF8Encoding]::new($false));$events.AutoFlush=$true
$serial=[IO.Ports.SerialPort]::new($Port,115200,[IO.Ports.Parity]::None,8,[IO.Ports.StopBits]::One)
$serial.DtrEnable=$false;$serial.RtsEnable=$false;$serial.Handshake=[IO.Ports.Handshake]::None;$serial.ReadBufferSize=1048576
$ping=[Net.NetworkInformation.Ping]::new();$pingTask=$null;$nextPing=0.0;$misses=0
$lastHeartbeat=$null;$heartbeatEnded=$false;$serialText='';$uartChars=0;$heartbeatCount=0;$pingCount=0;$pingFailures=0
$dumped=$false;$queue=[Collections.Generic.Queue[string]]::new();$nextCommand=0.0;$requests=[Collections.Generic.HashSet[string]]::new();$nextStatus=0.0
function Event([string]$Kind,[hashtable]$Fields){$Fields.kind=$Kind;$Fields.utc=[DateTime]::UtcNow.ToString('o');$Fields.elapsed_s=$clock.Elapsed.TotalSeconds;$events.WriteLine(($Fields|ConvertTo-Json -Compress))}
function Send-DiagnosticKey([string]$Key){
 if(-not $AllowSysRq -or $Key -cnotmatch '^[hwtm804R]$'){throw 'SysRq key is not permitted'}
 if($null -eq $serial -or -not $serial.IsOpen){Event 'sysrq_unavailable' @{key=$Key};return}
 try{$serial.BreakState=$true;Start-Sleep -Milliseconds 100;$serial.BreakState=$false;$serial.Write($Key);Event 'sysrq_sent' @{key=$Key}}
 catch{Event 'sysrq_error' @{key=$Key;error_type=$_.Exception.GetType().FullName}}
 finally{if($null -ne $serial -and $serial.IsOpen){try{$serial.BreakState=$false}catch{Event 'break_clear_error' @{}}}}
}
try{
 $serial.Open();Event 'ready' @{port=$Port;sysrq_enabled=[bool]$AllowSysRq;auto_dump=[bool]$AutoDumpOnLoss};Write-Output 'WATCH_READY'
 while($clock.Elapsed.TotalSeconds -lt $Seconds -and -not(Test-Path -LiteralPath (Join-Path $OutputDirectory 'STOP'))){
  $now=$clock.Elapsed.TotalSeconds
  if($null -ne $serial){
   try{
    $chunk=$serial.ReadExisting()
    if($chunk){
     $uartChars+=$chunk.Length;$uart.WriteLine((@{utc=[DateTime]::UtcNow.ToString('o');elapsed_s=$now;text=$chunk}|ConvertTo-Json -Compress))
     $serialText+=$chunk
     while(($nl=$serialText.IndexOf("`n")) -ge 0){
      $line=$serialText.Substring(0,$nl).TrimEnd("`r");$serialText=$serialText.Substring($nl+1)
      if($line -match ('NKHB '+[regex]::Escape($HeartbeatToken)+' ([0-9]+) ([0-9.]+) fps=([0-9]+)')){$lastHeartbeat=$now;$heartbeatCount++;Event 'heartbeat' @{seq=[int]$Matches[1];uptime=[double]$Matches[2];fps=[int]$Matches[3]}}
      if($line.Contains('NKHB_END '+$HeartbeatToken)){$heartbeatEnded=$true;Event 'heartbeat_end' @{}}
     }
     if($serialText.Length -gt 8192){$serialText=$serialText.Substring($serialText.Length-8192);Event 'line_buffer_trim' @{}}
    }
   }catch{Event 'serial_error' @{error_type=$_.Exception.GetType().FullName};try{$serial.Dispose()}catch{};$serial=$null}
  }
  if($null -ne $pingTask -and $pingTask.IsCompleted){
   try{$reply=$pingTask.GetAwaiter().GetResult();$status=$reply.Status.ToString();$rtt=$reply.RoundtripTime}catch{$status='TaskError';$rtt=$null}
   $pingCount++;if($status -eq 'Success'){$misses=0}else{$misses++;$pingFailures++}
   Event 'ping' @{status=$status;rtt_ms=$rtt};$pingTask=$null
  }
  if($null -eq $pingTask -and $now -ge $nextPing){$pingTask=$ping.SendPingAsync($DeviceHost,400);$nextPing=$now+1}
  $request=Join-Path $OutputDirectory 'REQUEST.txt'
  if(Test-Path -LiteralPath $request){
   $text=[IO.File]::ReadAllText($request).Trim()
   if($text.Length -le 80 -and $text -cmatch '^([a-zA-Z0-9-]{1,48}) ([hwtm804R])$'){
    $requestId=$Matches[1];$key=$Matches[2]
    if($requests.Add($requestId)){if($AllowSysRq){$queue.Enqueue($key)}else{Event 'sysrq_request_rejected' @{reason='not-enabled'}}}
   }
  }
  if($AutoDumpOnLoss -and -not $dumped -and -not $heartbeatEnded -and $null -ne $lastHeartbeat -and $misses -ge 3 -and ($now-$lastHeartbeat) -ge 3){
   foreach($key in @('8','w','t','m','0')){$queue.Enqueue($key)};$dumped=$true;Event 'auto_dump_triggered' @{misses=$misses;heartbeat_age_s=($now-$lastHeartbeat)}
  }
  if($queue.Count -gt 0 -and $now -ge $nextCommand){Send-DiagnosticKey ($queue.Dequeue());$nextCommand=$now+2}
  if($now -ge $nextStatus){Write-Output ("WATCH elapsed={0:N0}s uart={1} heartbeat={2} ping_failures={3}" -f $now,$uartChars,$heartbeatCount,$pingFailures);$nextStatus=$now+15}
  Start-Sleep -Milliseconds 20
 }
}finally{
 if($null -ne $serial){try{if($serial.IsOpen){$serial.Close()}}finally{$serial.Dispose()}}
 $ping.Dispose();$uart.Dispose();Event 'closed' @{uart_chars=$uartChars;heartbeats=$heartbeatCount;heartbeat_ended=$heartbeatEnded;ping_count=$pingCount;ping_failures=$pingFailures};$events.Dispose()
 Write-Output 'WATCH_CLOSED'
}
