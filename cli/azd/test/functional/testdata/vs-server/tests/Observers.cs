
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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

class Recorder<T> : IObserver<T>
{
    public List<T> Values = new List<T>();
    public Exception? Error;

    public void OnCompleted(){}
    public void OnError(Exception e) => Error = e;
    public void OnNext(T value) {
        Values.Add(value);
    }
}
