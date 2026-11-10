# disklens benchmark harness: compares disklens against gdu, dust and dua-cli
# (ncdu has no native Windows build; on Windows it runs under WSL).
# All tools run in apparent-size mode for a like-for-like comparison.
# Usage: powershell -File scripts/bench.ps1 [-Runs 3] [-Quick]
param(
    [int]$Runs = 3,
    [switch]$Quick
)

$disklens = if (Test-Path "$PSScriptRoot\..\disklens.exe") { "$PSScriptRoot\..\disklens.exe" } else { "$PSScriptRoot\..\disklens-next.exe" }
$links = "$env:LOCALAPPDATA\Microsoft\WinGet\Links"

$targets = @(
    @{ Name = "Desktop"; Path = "$env:USERPROFILE\Desktop" },
    @{ Name = "Downloads"; Path = "$env:USERPROFILE\Downloads" },
    @{ Name = "AppData\Local (caches)"; Path = "$env:LOCALAPPDATA" }
)
# Add your own heavy trees here, e.g. a node_modules or ML models directory:
# $targets += @{ Name = "node_modules (project)"; Path = "C:\path\to\node_modules" }
$targets = $targets | Where-Object { Test-Path $_.Path }
if ($Quick) { $targets = $targets[0..([Math]::Min(1, $targets.Count - 1))] }

$tools = [ordered]@{
    disklens = { param($p) & $disklens --summarize --apparent --no-tui $p 2>$null | Out-Null }
    gdu      = { param($p) & "$links\gdu.exe" -a -p -o NUL $p 2>$null | Out-Null }
    dust     = { param($p) & "$links\dust.exe" -s -d 1 $p 2>$null | Out-Null }
    dua      = { param($p) & "$links\dua.exe" ifloor -A $p 2>$null | Out-Null }
}

Write-Output "benchmark: $Runs measured runs after 1 warmup run; apparent-size mode; warm FS cache"
foreach ($t in $targets) {
    Write-Output ""
    Write-Output "## $($t.Name) - $($t.Path)"
    Write-Output "| tool | mean | min | max | vs disklens |"
    Write-Output "|---|---|---|---|---|"
    $base = 0.0
    foreach ($name in $tools.Keys) {
        $cmd = $tools[$name]
        & $cmd $t.Path | Out-Null # warmup
        $times = @()
        for ($i = 0; $i -lt $Runs; $i++) {
            $sw = [Diagnostics.Stopwatch]::StartNew()
            & $cmd $t.Path | Out-Null
            if ($LASTEXITCODE -ne 0 -and $name -ne 'dua') { Write-Warning "$name exit=$LASTEXITCODE" }
            $sw.Stop()
            $times += $sw.Elapsed.TotalSeconds
        }
        $sorted = $times | Sort-Object
        $mean = ($times | Measure-Object -Average).Average
        $min = $sorted[0]
        $max = $sorted[-1]
        if ($name -eq 'disklens') { $base = $mean }
        $inv = [Globalization.CultureInfo]::InvariantCulture
        $ratio = if ($base -gt 0 -and $name -ne 'disklens') { ($mean / $base).ToString("0.00", $inv) + "x" } else { "1.00x" }
        Write-Output ("| {0} | {1}s | {2}s | {3}s | {4} |" -f $name,
            $mean.ToString("0.00", $inv), $min.ToString("0.00", $inv), $max.ToString("0.00", $inv), $ratio)
    }
}
