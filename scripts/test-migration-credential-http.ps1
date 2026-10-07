[CmdletBinding()]
param([Parameter(Mandatory)][string]$VerifierPath)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$migrationTokens = $null
$migrationErrors = $null
$migrationAst = [Management.Automation.Language.Parser]::ParseFile($VerifierPath, [ref]$migrationTokens, [ref]$migrationErrors)
if ($migrationErrors.Count) { throw 'Credential verifier parse failed.' }
$migrationFunction = $migrationAst.Find({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq 'Invoke-CredentialMetadataGet'
}, $false)
if ($null -eq $migrationFunction) { throw 'Credential HTTP helper is missing.' }
. ([ScriptBlock]::Create($migrationFunction.Extent.Text))

if (-not ('SflCredentialScopeHttpFixture' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading.Tasks;
public sealed class SflCredentialScopeHttpFixture : IDisposable {
    private readonly TcpListener listener;
    private readonly Task worker;
    public int Port { get; private set; }
    public SflCredentialScopeHttpFixture(string scopeHeader) {
        listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        Port = ((IPEndPoint)listener.LocalEndpoint).Port;
        worker = Task.Run(() => {
            using (var client = listener.AcceptTcpClient())
            using (var stream = client.GetStream())
            using (var reader = new StreamReader(stream, Encoding.ASCII, false, 1024, true)) {
                if (reader.ReadLine() != "GET /user HTTP/1.1") throw new InvalidOperationException("Unexpected fixture method or path.");
                bool authorized = false;
                for (int count = 0; count < 100; count++) {
                    string line = reader.ReadLine();
                    if (line == null || line.Length > 8192) throw new InvalidOperationException("Invalid fixture request.");
                    if (line.Length == 0) break;
                    if (line == "Authorization: Bearer synthetic-private-token") authorized = true;
                    if (count == 99) throw new InvalidOperationException("Fixture request exceeded its limit.");
                }
                if (!authorized) throw new InvalidOperationException("Unexpected fixture authentication.");
                string body = "{\"login\":\"HemSoft\"}";
                string header = scopeHeader == null ? "" : "X-OAuth-Scopes: " + scopeHeader + "\r\n";
                byte[] response = Encoding.ASCII.GetBytes("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n" + header +
                    "Content-Length: " + Encoding.ASCII.GetByteCount(body) + "\r\nConnection: close\r\n\r\n" + body);
                stream.Write(response, 0, response.Length);
            }
        });
    }
    public void Complete() {
        if (!worker.Wait(TimeSpan.FromSeconds(5))) throw new InvalidOperationException("Fixture response timed out.");
    }
    public void Dispose() { listener.Stop(); }
}
'@
}
$migrationCases = @(
    @{ name = 'single-scope'; header = 'repo'; expected = 'repo'; count = 1 },
    @{ name = 'multiple-scopes'; header = 'repo, workflow'; expected = 'repo,workflow'; count = 2 },
    @{ name = 'missing-header'; header = $null; expected = ''; count = 0 },
    @{ name = 'blank-header'; header = ' '; expected = ''; count = 0 }
)
foreach ($migrationCase in $migrationCases) {
    $migrationServer = [SflCredentialScopeHttpFixture]::new($migrationCase.header)
    try {
        $migrationResult = Invoke-CredentialMetadataGet -Uri "http://127.0.0.1:$($migrationServer.Port)/user" -Token 'synthetic-private-token'
        $migrationServer.Complete()
        # Exercise the real parser and the exact strict-mode Count access used
        # by the GitHubToken branch, rather than supplying an already typed mock.
        if ($migrationResult.scopes.Count -ne $migrationCase.count -or
            ($migrationResult.scopes -join ',') -cne $migrationCase.expected -or
            $migrationResult.data.login -cne 'HemSoft') {
            throw "Unexpected HTTP scope parsing: $($migrationCase.name)"
        }
    } finally {
        $migrationServer.Dispose()
    }
}
Write-Output "PASS: $($migrationCases.Count) actual HTTP scope-cardinality cases."
