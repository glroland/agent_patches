import { Router } from 'express';
import * as chatController from '../controllers/chatController.js';

const router = Router();

router.post('/', chatController.broadcastMessage);
router.post('/central', chatController.centralChatMessage);
router.get('/history', chatController.getChatHistory);
router.delete('/history/:chatId', chatController.clearChatHistory);

export default router;
