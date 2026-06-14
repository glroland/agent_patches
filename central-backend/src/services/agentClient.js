// Client for talking to an individual endpoint-server's A2A JSON-RPC API
// (see endpoint-server/a2a). This will be used by the poller to pull status
// from each agent and to relay operator messages/tasks to a specific agent.
//
// Not yet implemented — method signatures are sketched out for the routes
// and poller that will eventually call them.

export class AgentClient {
  constructor(/* { baseUrl, authToken } */) {
    throw new Error('AgentClient: not implemented');
  }

  // Fetches the agent card from /.well-known/agent.json.
  async getAgentCard() {
    throw new Error('getAgentCard: not implemented');
  }

  // Sends a natural-language task/message to the agent and returns its reply.
  async sendTask(/* message */) {
    throw new Error('sendTask: not implemented');
  }
}
