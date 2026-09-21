$ErrorActionPreference = "Continue"
$token = "eyJhbGciOiJSUzI1NiIsImtpZCI6Im15ZkV6cDc4M0tpX0pDeDhWbmMzWF9peDZqWnJiNkNmNU9Na0daTVBJM3MiLCJ0eXAiOiJKV1QiLCJ2ZXIiOiJ2MiJ9.eyJhY3IiOiIwIiwiYWxsb3dlZC1vcmlnaW5zIjpbIioiXSwiYXVkIjoiYWNjb3VudCIsImF1dGhfdGltZSI6MTc4OTU2NzIyMSwiYXpwIjoiY29uc29sZSIsImVtYWlsIjoiIiwiZW1haWxfdmVyaWZpZWQiOmZhbHNlLCJleHAiOjE3OTI1MDQ4MDIsImlhdCI6MTc4OTkxMjgwMiwiaXNzIjoiaHR0cHM6Ly93d3cuY29kZWJ1ZGR5LmNuL2F1dGgvcmVhbG1zL2NvcGlsb3QiLCJqdGkiOiI5OTY2NTFlNC1lMjY2LTRmYjMtODRiMC1mYzJiN2ZkMTc0ZDAiLCJuaWNrbmFtZSI6Im15c3FsIiwib25laWRfdW5pb25faWQiOiIiLCJwcmVmZXJyZWRfdXNlcm5hbWUiOiIxODgwNTI5MzEyMSIsInJlYWxtX2FjY2VzcyI6eyJyb2xlcyI6WyJvZmZsaW5lX2FjY2VzcyIsImRlZmF1bHQtcm9sZXMiLCJ1bWFfYXV0aG9yaXphdGlvbiJdfSwicmVzb3VyY2VfYWNjZXNzIjp7ImFjY291bnQiOnsicm9sZXMiOlsidmlldy1wcm9maWxlIiwibWFuYWdlLWFjY291bnQtbGlua3MiLCJtYW5hZ2UtYWNjb3VudC1saW5rcyJdfX0sInNjb3BlIjoib3BlbmlkIGFjciBwcm9maWxlIGJhc2ljIHdlYi1vcmlnaW5zIHJvbGVzIG9mZmxpbmVfYWNjZXNzIGVtYWlsIiwic2Vzc2lvbl9zdGF0ZSI6IjIxNjljYzk4LTgyZjktNGI2OC1hYThmLTcyMGY4NWNlZDkzYSIsInNpZCI6IjIxNjljYzk4LTgyZjktNGI2OC1hYThmLTcyMGY4NWNlZDkzYSIsInN1YiI6IjJhNDkzNjJhLWM4ZjQtNDgyNC1iMjhiLTM0M2YzNjI4OTA1NiIsInN1Yl90eXBlIjoidXNlciIsInRva2VuX3NvdXJjZSI6ImVudGVycHJpc2Vfc3dpdGNoIiwidHlwIjoiQmVhcmVyIn0.GaAKRCyDLKwNkUuvyJvJJM6y16XpNStPf5y9rHWas7UNqqHEdP-IEA48IdaVq4kZC_MCQ2oGVvjNtss7BJ054QKZAJ8NAXEBUCnxujlcH-ZMx2gLF06fT8RaD692Z4Yi6RJYiX8cs-WmO45XvZ3KgM6F4VQDKacC_1BIAqHdt2p6_GS5XyaJpiu4a9aqtHMIBUWTcWCWF4tQp52Bw-4tyS4xr9crT4omMkbP3GOt0QrVywO57TanLxBHwxZLBX71mpFQeJScG-Gt8a65kL1gXPDypTWp6dZ3r6R4lbezZzEzLrugm9dqUGO6LP-jj240HoGYDEYq7pEBDRwSHFku_Q"
$base = "https://www.codebuddy.cn/v2/activity/growth"
$auth = "Authorization: Bearer $token"
$common = @("-H", $auth, "-H", "Accept: application/json", "-H", "X-User-Id: 2a49362a-c8f4-4824-b28b-343f36289056", "-H", "X-Domain: www.codebuddy.cn")

Write-Host "=== [DIRECT bypass proxy] GET buddy/travel/status ==="
curl.exe -sS --noproxy "*" -i -X GET "$base/buddy/travel/status" @common

Write-Host ""
Write-Host "=== [DIRECT bypass proxy] GET buddy/travel/config ==="
curl.exe -sS --noproxy "*" -X GET "$base/buddy/travel/config" @common

Write-Host ""
Write-Host "=== [DIRECT bypass proxy] GET tasks (compact) ==="
$out = curl.exe -sS --noproxy "*" -X GET "$base/tasks" @common
Write-Host $out

