class WriterObserver<ProgressMessage> : IObserver<ProgressMessage>
{
    public void OnCompleted() => Console.WriteLine("Completed");
    public void OnError(Exception error) => Console.WriteLine($"Error: {error}");
    public void OnNext(ProgressMessage value) {
        var msg = value!.ToString()!;
        if (msg[msg.Length-1] == '\n') {
            Console.Write(msg);
        } else {
            Console.WriteLine(msg);
        }
    }
}
