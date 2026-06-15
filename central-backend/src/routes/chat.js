import { Router } from 'express';
import * as chatController from '../controllers/chatController.js';

const router = Router();

router.post('/', chatController.broadcastMessage);

export default router;
