using System.CommandLine;
using Microsoft.Azd;
using Spectre.Console;

public class ListenCommand
{
    private readonly AzdClient _azdClient;

    public ListenCommand(AzdClient azdClient)
    {
        _azdClient = azdClient;
    }

    public Command Build()
    {
        var cmd = new Command("listen", "Starts the extension and listens for events.");

        cmd.SetHandler(async context =>
        {
            var cancellationToken = context.GetCancellationToken();

            await using var eventManager = new EventManager(_azdClient);

            // === Project Event Handler: preprovision
            await eventManager.AddProjectEventHandlerAsync("preprovision", async args =>
            {
                for (int i = 1; i <= 20; i++)
                {
                    AnsiConsole.MarkupLine($"[green]{i}.[/] Doing important work in C# extension...");
                    await Task.Delay(250, cancellationToken);
                }
            }, cancellationToken);

            // === Service Event Handler: prepackage
            await eventManager.AddServiceEventHandlerAsync("prepackage", async args =>
            {
                for (int i = 1; i <= 20; i++)
                {
                    AnsiConsole.MarkupLine($"[blue]{i}.[/] Doing important work in C# extension...");
                    await Task.Delay(250, cancellationToken);
                }
            }, options: null, cancellationToken);

            // === Start listening (blocking call)
            try
            {
                await eventManager.ReceiveAsync(cancellationToken);
            }
            catch (Exception ex)
            {
                AnsiConsole.MarkupLineInterpolated($"[red]Error while receiving events:[/] {ex.Message}");
            }
        });

        return cmd;
    }
}
